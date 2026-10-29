package attestation

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-sev-guest/abi"
	sevtesting "github.com/google/go-sev-guest/testing"
	"github.com/verirelay/protocol/avr1"
)

var (
	testPublicKey = ed25519.PublicKey([]byte("0123456789abcdef0123456789abcdef"))
	testNonce     = []byte("abcdef0123456789abcdef0123456789")
)

func TestManagerBuildsBoundAttestationResponse(t *testing.T) {
	reportData, err := avr1.ReportDataAVR1(testPublicKey, testNonce)
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManagerWithOptions(FakeProvider{}, ManagerOptions{DisclosureReader: testDisclosureReader{}})

	response, err := manager.Attestation(context.Background(), Request{Nonce: testNonce, ReceiptPublicKey: testPublicKey, KeyID: avr1.ReceiptKeyID(testPublicKey)})
	if err != nil {
		t.Fatal(err)
	}

	if response.Version != avr1.AVRVersion || response.TEEType != avr1.AttestationTEETypeSEVSNP {
		t.Fatalf("unexpected metadata: %+v", response)
	}
	if response.Nonce != b64(testNonce) || response.ReceiptSigningPublicKey != b64(testPublicKey) || response.KeyID != avr1.ReceiptKeyID(testPublicKey) {
		t.Fatalf("response did not expose bound nonce/key metadata: %+v", response)
	}
	wantBinding := avr1.AttestationKeyBinding{
		Rule:               avr1.AttestationBindingRuleV1,
		ProtocolVersion:    avr1.AVRVersion,
		SignatureAlgorithm: avr1.AVRSignatureAlgorithm,
		ReportDataSHA512:   hex.EncodeToString(reportData[:]),
		ReportDataEncoding: avr1.AttestationReportDataEncoding,
		PublicKeyEncoding:  avr1.AttestationPublicKeyEncoding,
		NonceEncoding:      avr1.AttestationNonceEncoding,
	}
	if response.KeyBinding != wantBinding {
		t.Fatalf("key binding = %+v, want %+v", response.KeyBinding, wantBinding)
	}
	report, err := base64.RawURLEncoding.DecodeString(response.Evidence.Report)
	if err != nil || len(report) != avr1.AttestationReportSize || !bytes.Equal(report[0x50:0x90], reportData[:]) {
		t.Fatalf("evidence report = %d bytes, err %v", len(report), err)
	}
	if response.Evidence.Format != avr1.AttestationEvidenceFormat || response.Evidence.ReportData != hex.EncodeToString(reportData[:]) {
		t.Fatalf("unexpected evidence: %+v", response.Evidence)
	}
	if response.Endorsements.Format != avr1.AttestationEndorsementsFormat || response.Endorsements.Encoding != avr1.AttestationEndorsementsEncoding || response.Endorsements.VCEK != b64([]byte("fake-vcek-der")) || response.Endorsements.VLEK != "" {
		t.Fatalf("unexpected endorsements: %+v", response.Endorsements)
	}
	if want := testManifestDisclosure(); response.Source != want.Source || response.LaunchProfile != want.LaunchProfile {
		t.Fatalf("unexpected disclosure: source=%+v launch_profile=%+v", response.Source, response.LaunchProfile)
	}

	raw := jsonObject(t, response)
	if len(raw) != 10 {
		t.Fatalf("unexpected JSON shape: %+v", raw)
	}
	if _, ok := raw["bootstrap"]; ok {
		t.Fatal("bootstrap disclosed without a Bootstrap")
	}
}

func TestManagerDisclosesBootstrap(t *testing.T) {
	bootstrap := []byte(`{"version":"veriprov-bootstrap/1"}`)
	manager := NewManagerWithOptions(FakeProvider{}, ManagerOptions{DisclosureReader: testDisclosureReader{}, Bootstrap: bootstrap})
	response, err := manager.Attestation(context.Background(), Request{Nonce: testNonce, ReceiptPublicKey: testPublicKey})
	if err != nil {
		t.Fatal(err)
	}
	if response.Bootstrap != b64(bootstrap) || jsonObject(t, response)["bootstrap"] != b64(bootstrap) {
		t.Fatalf("bootstrap = %q", response.Bootstrap)
	}
}

func TestManagerFailsClosedWhenDisclosureReaderFails(t *testing.T) {
	manager := NewManagerWithOptions(FakeProvider{}, ManagerOptions{DisclosureReader: failingDisclosureReader{err: ErrInvalidManifest}})
	if _, err := manager.Attestation(context.Background(), Request{Nonce: testNonce, ReceiptPublicKey: testPublicKey}); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("disclosure err = %v", err)
	}
}

func TestManagerRejectsInvalidBindingInputs(t *testing.T) {
	manager := NewManagerWithOptions(FakeProvider{}, ManagerOptions{DisclosureReader: testDisclosureReader{}})
	if _, err := manager.Attestation(context.Background(), Request{Nonce: []byte("short"), ReceiptPublicKey: testPublicKey}); err == nil {
		t.Fatal("short nonce was accepted")
	}
}

func TestManagerRejectsAReportOfTheWrongSize(t *testing.T) {
	for _, size := range []int{0, avr1.AttestationReportSize - 1, avr1.AttestationReportSize + 1} {
		manager := NewManagerWithOptions(sizedReportProvider{size: size}, ManagerOptions{DisclosureReader: testDisclosureReader{}})
		if _, err := manager.Attestation(context.Background(), Request{Nonce: testNonce, ReceiptPublicKey: testPublicKey}); !errors.Is(err, ErrInvalidAttestationReport) {
			t.Fatalf("%d-byte report err = %v", size, err)
		}
	}
}

func TestSEVSNPEndorsementsRequireCompleteCertificateChain(t *testing.T) {
	endorsements, err := NewSEVSNPEndorsements([]byte("vcek"), nil, []byte("ask"), []byte("ark"))
	if err != nil || endorsements.VCEK != b64([]byte("vcek")) || endorsements.VLEK != "" {
		t.Fatalf("vcek chain = %+v, %v", endorsements, err)
	}
	if _, err := NewSEVSNPEndorsements(nil, []byte("vlek"), []byte("ask"), []byte("ark")); err != nil {
		t.Fatalf("vlek chain rejected: %v", err)
	}
	for _, tc := range []struct {
		name           string
		vcek, ask, ark []byte
	}{
		{name: "missing leaf", ask: []byte("ask"), ark: []byte("ark")},
		{name: "missing ask", vcek: []byte("vcek"), ark: []byte("ark")},
		{name: "missing ark", vcek: []byte("vcek"), ask: []byte("ask")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewSEVSNPEndorsements(tc.vcek, nil, tc.ask, tc.ark); !errors.Is(err, ErrIncompleteEndorsements) {
				t.Fatalf("endorsements err = %v", err)
			}
		})
	}
}

func TestHostDataReadsTheReportField(t *testing.T) {
	report := sevtesting.TestRawReportV3([64]byte{}, 0x00a00f10)
	want := sha256.Sum256([]byte("bootstrap"))
	copy(report[0xc0:0xe0], want[:])
	got, err := HostData(report[:abi.ReportSize])
	if err != nil || !bytes.Equal(got, want[:]) {
		t.Fatalf("HostData = %x, %v; want %x", got, err, want)
	}
	if _, err := HostData(report[:abi.ReportSize-1]); !errors.Is(err, ErrInvalidAttestationReport) {
		t.Fatalf("short report err = %v", err)
	}
}

func jsonObject(t *testing.T, value any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

type testDisclosureReader struct{}

func (testDisclosureReader) ReadManifest(context.Context) (ManifestDisclosure, error) {
	return testManifestDisclosure(), nil
}

type failingDisclosureReader struct{ err error }

func (r failingDisclosureReader) ReadManifest(context.Context) (ManifestDisclosure, error) {
	return ManifestDisclosure{}, r.err
}

type sizedReportProvider struct{ size int }

func (p sizedReportProvider) Quote(context.Context, QuoteRequest) (Quote, error) {
	endorsements, err := NewSEVSNPEndorsements([]byte("vcek"), nil, []byte("ask"), []byte("ark"))
	return Quote{RawReport: make([]byte, p.size), Endorsements: endorsements}, err
}
