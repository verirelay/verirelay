package provisioning

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-sev-guest/abi"
	sevtesting "github.com/google/go-sev-guest/testing"
	"github.com/verirelay/protocol/guest"
	"github.com/verirelay/protocol/veriprov1"

	"github.com/verirelay/verirelay/internal/config"
)

const testRuntimeConfig = `{"version":"verirelay-runtime-config/1","admin_token":"test-only-admin-token","future_field":true,"raw_store":{"type":"memory","max_bytes":1024}}`

// testReporter returns go-sev-guest test reports carrying the requested
// REPORT_DATA and a fixed HOST_DATA.
type testReporter struct {
	mu         sync.Mutex
	hostData   [32]byte
	reportData [][64]byte
}

func (r *testReporter) Report(_ context.Context, reportData [64]byte) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reportData = append(r.reportData, reportData)
	raw := sevtesting.TestRawReportV3(reportData, 0x00a00f10)
	report := append([]byte(nil), raw[:abi.ReportSize]...)
	copy(report[0xc0:0xe0], r.hostData[:])
	return report, nil
}

// testVeriProv serves veriprov/1 and signs envelopes with identity.
type testVeriProv struct {
	t         *testing.T
	identity  ed25519.PrivateKey
	plaintext string
	expiresAt time.Time
	unsigned  bool
	requests  int
	report    []byte
}

func newTestVeriProv(t *testing.T) (*testVeriProv, *httptest.Server) {
	t.Helper()
	_, identity, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	veriProv := &testVeriProv{t: t, identity: identity, plaintext: testRuntimeConfig, expiresAt: time.Now().Add(time.Minute)}
	server := httptest.NewTLSServer(veriProv)
	t.Cleanup(server.Close)
	return veriProv, server
}

func (v *testVeriProv) identityPublicKey() ed25519.PublicKey {
	return v.identity.Public().(ed25519.PublicKey)
}

func (v *testVeriProv) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	v.requests++
	challengeID := veriprov1.EncodeBase64URL(bytes.Repeat([]byte{7}, 32))
	nonce := bytes.Repeat([]byte{9}, veriprov1.NonceSize)
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/v1/challenge":
		issuedAt := v.expiresAt.Add(-time.Minute)
		_ = json.NewEncoder(w).Encode(veriprov1.ChallengeResponse{
			ChallengeID: challengeID, Nonce: veriprov1.EncodeBase64URL(nonce),
			IssuedAt: veriprov1.FormatUTCTime(issuedAt), ExpiresAt: veriprov1.FormatUTCTime(v.expiresAt), TTLSeconds: 60,
		})
	case "/v1/provision":
		var request veriprov1.ProvisionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			v.t.Error(err)
			return
		}
		public, err := veriprov1.DecodeBase64URL(request.ProvisioningPublicKey, "provisioning_public_key", veriprov1.X25519PublicKeySize)
		if err != nil {
			v.t.Error(err)
			return
		}
		if v.report, err = veriprov1.DecodeBase64URL(request.AttestationReport, "attestation_report", veriprov1.AttestationReportSize); err != nil {
			v.t.Error(err)
			return
		}
		want, err := veriprov1.ReportData(nonce, public)
		if err != nil || !bytes.Equal(v.report[0x50:0x90], want[:]) {
			v.t.Error("attestation report does not bind the challenge nonce and provisioning key")
			return
		}
		envelope, err := veriprov1.Seal(public, []byte(v.plaintext), veriprov1.AADContext{Protocol: veriprov1.ProtocolVersion, ChallengeID: request.ChallengeID})
		if err != nil {
			v.t.Error(err)
			return
		}
		if v.unsigned {
			envelope.Signature = veriprov1.EncodeBase64URL(make([]byte, ed25519.SignatureSize))
		} else if err := veriprov1.SignEnvelope(v.identity, &envelope); err != nil {
			v.t.Error(err)
			return
		}
		_ = json.NewEncoder(w).Encode(veriprov1.ProvisionResponse{Envelope: envelope})
	default:
		http.NotFound(w, r)
	}
}

func testClient(t *testing.T, server *httptest.Server, identity ed25519.PublicKey) *Client {
	t.Helper()
	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{BaseURL: baseURL, HTTPClient: server.Client(), Reporter: &testReporter{}, IdentityKey: identity}
}

func TestClientFetchRuntimeConfig(t *testing.T) {
	veriProv, server := newTestVeriProv(t)
	got, err := testClient(t, server, veriProv.identityPublicKey()).FetchRuntimeConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != veriprov1.RuntimeConfigVersion || got.AdminToken != "test-only-admin-token" || got.RawStore == nil || *got.RawStore.MaxBytes != 1024 {
		t.Fatalf("runtime config = %+v", got)
	}
}

func TestClientDoesNotJudgeChallengeExpiry(t *testing.T) {
	veriProv, server := newTestVeriProv(t)
	veriProv.expiresAt = time.Now().Add(-time.Hour)
	if _, err := testClient(t, server, veriProv.identityPublicKey()).FetchRuntimeConfig(context.Background()); err != nil {
		t.Fatalf("challenge expired by the Guest's clock was rejected: %v", err)
	}
}

func TestClientRejectsEnvelopeNotSignedByTheIdentityKey(t *testing.T) {
	veriProv, server := newTestVeriProv(t)
	other, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testClient(t, server, other).FetchRuntimeConfig(context.Background()); !errors.Is(err, ErrEnvelopeSignature) {
		t.Fatalf("foreign signature err = %v", err)
	}
	veriProv.unsigned = true
	if _, err := testClient(t, server, veriProv.identityPublicKey()).FetchRuntimeConfig(context.Background()); !errors.Is(err, ErrEnvelopeSignature) {
		t.Fatalf("invalid signature err = %v", err)
	}
}

func TestClientReportsVeriProvError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Request-ID", "vp-test")
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"error":{"code":"policy.denied","message":"attestation policy denied the report","request_id":"vp-test"}}`))
	}))
	defer server.Close()
	_, err := testClient(t, server, nil).FetchRuntimeConfig(context.Background())
	if !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "HTTP 409") || !strings.Contains(err.Error(), "policy.denied") || !strings.Contains(err.Error(), "request_id=vp-test") {
		t.Fatalf("error = %v", err)
	}
}

func productionGuest(t *testing.T, server *httptest.Server, bootstrap []byte, hostData [32]byte) (Guest, *testReporter) {
	t.Helper()
	reporter := &testReporter{hostData: hostData}
	disk := append(append([]byte(nil), bootstrap...), make([]byte, 4096)...)
	return Guest{
		ReadConfigDisk: func() ([]byte, error) { return disk, nil },
		Reporter:       reporter,
		HTTPClient:     server.Client(),
	}, reporter
}

func testBootstrap(t *testing.T, server *httptest.Server, identity ed25519.PublicKey) []byte {
	t.Helper()
	bootstrap, err := json.Marshal(guest.Bootstrap{
		Version: guest.BootstrapVersion, Protocol: guest.BootstrapProtocol,
		URL: server.URL, IdentityPublicKey: veriprov1.EncodeBase64URL(identity), Profile: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return bootstrap
}

func productionConfig() config.Config {
	return config.Config{Production: true, ListenAddr: ":8080", Provisioning: config.ProvisioningConfig{Timeout: 5 * time.Second}}
}

func TestResolveProductionProvisionsFromTheBootstrap(t *testing.T) {
	veriProv, server := newTestVeriProv(t)
	bootstrap := testBootstrap(t, server, veriProv.identityPublicKey())
	platform, reporter := productionGuest(t, server, bootstrap, guest.HostData(bootstrap))

	got, err := Resolve(context.Background(), productionConfig(), platform)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bootstrap, bootstrap) || got.AdminToken != "test-only-admin-token" || got.RawStore.MaxBytes != 1024 || got.ListenAddr != ":8080" {
		t.Fatalf("effective config = %+v", got)
	}
	if len(reporter.reportData) != 2 || reporter.reportData[0] != [64]byte{} {
		t.Fatalf("report requests = %d, first REPORT_DATA %x", len(reporter.reportData), reporter.reportData[0])
	}
}

func TestResolveProductionRefusesAHostDataMismatch(t *testing.T) {
	veriProv, server := newTestVeriProv(t)
	bootstrap := testBootstrap(t, server, veriProv.identityPublicKey())
	platform, _ := productionGuest(t, server, bootstrap, guest.HostData([]byte("another bootstrap")))
	if _, err := Resolve(context.Background(), productionConfig(), platform); !errors.Is(err, ErrHostDataMismatch) {
		t.Fatalf("err = %v", err)
	}
	if veriProv.requests != 0 {
		t.Fatalf("VeriProv was contacted %d times despite the mismatch", veriProv.requests)
	}
}

func TestResolveProductionRejectsAnInvalidConfigDisk(t *testing.T) {
	_, server := newTestVeriProv(t)
	for name, disk := range map[string][]byte{
		"no padding":     []byte(`{"version":"veriprov-bootstrap/1"}`),
		"empty":          make([]byte, 64),
		"unknown field":  append([]byte(`{"version":"veriprov-bootstrap/1","protocol":"veriprov/1","url":"https://veriprov.example","identity_public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","tls_spki_sha256":"x"}`), 0),
		"read failure":   nil,
		"not an origin":  append([]byte(`{"version":"veriprov-bootstrap/1","protocol":"veriprov/1","url":"https://veriprov.example/path","identity_public_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`), 0),
		"short identity": append([]byte(`{"version":"veriprov-bootstrap/1","protocol":"veriprov/1","url":"https://veriprov.example","identity_public_key":"AAAA"}`), 0),
	} {
		t.Run(name, func(t *testing.T) {
			platform := Guest{Reporter: &testReporter{}, HTTPClient: server.Client(), ReadConfigDisk: func() ([]byte, error) {
				if disk == nil {
					return nil, errors.New("no virtio block device")
				}
				return disk, nil
			}}
			if _, err := Resolve(context.Background(), productionConfig(), platform); !errors.Is(err, ErrInvalidBootstrap) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestResolveDevelopment(t *testing.T) {
	t.Run("local runtime config", func(t *testing.T) {
		cfg := config.Config{Provisioning: config.ProvisioningConfig{RuntimeConfig: []byte(testRuntimeConfig)}}
		got, err := Resolve(context.Background(), cfg, Guest{})
		if err != nil || got.AdminToken != "test-only-admin-token" || got.Bootstrap != nil {
			t.Fatalf("config = %+v, %v", got, err)
		}
	})
	t.Run("invalid local runtime config", func(t *testing.T) {
		cfg := config.Config{Provisioning: config.ProvisioningConfig{RuntimeConfig: []byte(`{"version":"verirelay-runtime-config/1","admin_token":"a","admin_token":"b"}`)}}
		if _, err := Resolve(context.Background(), cfg, Guest{}); !errors.Is(err, config.ErrInvalidRuntimeConfig) {
			t.Fatalf("duplicate key err = %v", err)
		}
	})
	t.Run("veriprov", func(t *testing.T) {
		veriProv, server := newTestVeriProv(t)
		cfg := config.Config{Provisioning: config.ProvisioningConfig{Timeout: 5 * time.Second, VeriProvURL: server.URL, IdentityPublicKey: veriProv.identityPublicKey()}}
		got, err := Resolve(context.Background(), cfg, Guest{Reporter: &testReporter{}, HTTPClient: server.Client()})
		if err != nil || got.AdminToken != "test-only-admin-token" || got.Bootstrap != nil {
			t.Fatalf("config = %+v, %v", got, err)
		}
	})
	t.Run("none", func(t *testing.T) {
		cfg := config.Config{ListenAddr: "127.0.0.1:1"}
		got, err := Resolve(context.Background(), cfg, Guest{})
		if err != nil || got.ListenAddr != cfg.ListenAddr || got.AdminToken != "" {
			t.Fatalf("config = %+v, %v", got, err)
		}
	})
}
