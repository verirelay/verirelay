package proxy

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/verirelay/protocol/avr1"
	"github.com/verirelay/protocol/guest"
	"github.com/verirelay/verirelay/internal/attestation"
	"github.com/verirelay/verirelay/internal/storage"
)

type requestOption func(*http.Request)

const testAdminToken = "test-only-admin-token"

func withHeader(key, value string) requestOption {
	return func(r *http.Request) {
		r.Header.Set(key, value)
	}
}

func withProxyToken(token string) requestOption {
	return withHeader(proxyTokenHeader, token)
}

func serve(handler http.Handler, method, target string, body io.Reader, opts ...requestOption) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, body)
	for _, opt := range opts {
		opt(req)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// testUpstreamTLS is the TLS state of stubbed Upstream responses; a Receipt
// needs the Upstream SPKI.
var testUpstreamTLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{{RawSubjectPublicKeyInfo: []byte("test-spki")}}}

func stubResponse(req *http.Request, header http.Header, body io.ReadCloser) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: header, Body: body, Request: req, TLS: testUpstreamTLS}
}

// stubUpstream answers every forward with 200, header and body.
func stubUpstream(handler *Handler, header http.Header, body string) {
	handler.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return stubResponse(req, header.Clone(), io.NopCloser(strings.NewReader(body))), nil
	})}
}

func TestProxyRouteRequiresToken(t *testing.T) {
	var upstreamHits int
	var upstreamToken string
	var upstreamAuthorization string
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits++
		upstreamToken = r.Header.Get(proxyTokenHeader)
		upstreamAuthorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "upstream response")
	}))
	defer upstream.Close()

	for _, tc := range []struct {
		name            string
		token           string
		authorization   string
		emptyAdminToken bool
		wantStatus      int
	}{
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "wrong token", token: "wrong-token", wantStatus: http.StatusUnauthorized},
		{name: "bearer only", authorization: "Bearer " + testAdminToken, wantStatus: http.StatusUnauthorized},
		{name: "admin token unconfigured", token: "anything", emptyAdminToken: true, wantStatus: http.StatusUnauthorized},
		{name: "correct token", token: testAdminToken, authorization: "Bearer client-auth", wantStatus: http.StatusTeapot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := testHandler(t)
			if tc.emptyAdminToken {
				handler.options.AdminToken = ""
			}
			handler.client = upstream.Client()

			var opts []requestOption
			if tc.token != "" {
				opts = append(opts, withProxyToken(tc.token))
			}
			if tc.authorization != "" {
				opts = append(opts, withHeader("Authorization", tc.authorization))
			}
			response := serve(handler, http.MethodGet, upstreamRequestPath(t, upstream, "/test"), nil, opts...)
			if response.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.wantStatus, response.Body.String())
			}

			if tc.wantStatus == http.StatusUnauthorized {
				if decodeErrorResponse(t, response).Code != errorUnauthorized {
					t.Fatalf("error = %s", response.Body.String())
				}
				if response.Header().Get(requestIDHeader) != "" {
					t.Fatalf("unauthorized response has request id: %+v", response.Header())
				}
				if response.Header().Get("WWW-Authenticate") != "" {
					t.Fatalf("unauthorized proxy response has challenge: %+v", response.Header())
				}
				for _, token := range []string{testAdminToken, tc.token, strings.TrimPrefix(tc.authorization, "Bearer ")} {
					if token != "" && strings.Contains(response.Body.String(), token) {
						t.Fatalf("unauthorized response leaked token %q: %s", token, response.Body.String())
					}
				}
				if upstreamHits != 0 {
					t.Fatalf("unauthorized request hit upstream %d times", upstreamHits)
				}
				return
			}

			if response.Body.String() != "upstream response" || upstreamHits != 1 {
				t.Fatalf("forwarded response = %d %q, upstream hits=%d", response.Code, response.Body.String(), upstreamHits)
			}
			if upstreamToken != "" {
				t.Fatalf("proxy token reached upstream: %q", upstreamToken)
			}
			if upstreamAuthorization != "Bearer client-auth" {
				t.Fatalf("authorization = %q, want unchanged client header", upstreamAuthorization)
			}
		})
	}
}

func TestProxyForwardsToAnyPublicUpstream(t *testing.T) {
	var upstreamURLs []string
	handler := testHandler(t)
	handler.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		upstreamURLs = append(upstreamURLs, request.URL.String())
		return stubResponse(request, nil, io.NopCloser(strings.NewReader("ok"))), nil
	})}
	for _, target := range []string{"/to/api.example.com/v1/messages?test=true", "/to/https://other.example.com:8443/unlisted/"} {
		rr := serve(handler, http.MethodPost, target, nil, withProxyToken(testAdminToken))
		if rr.Code != http.StatusOK || rr.Body.String() != "ok" {
			t.Fatalf("%s = %d %q", target, rr.Code, rr.Body.String())
		}
	}
	if want := []string{"https://api.example.com/v1/messages?test=true", "https://other.example.com:8443/unlisted/"}; strings.Join(upstreamURLs, " ") != strings.Join(want, " ") {
		t.Fatalf("upstream URLs = %v, want %v", upstreamURLs, want)
	}
}

func TestRemovedRoutesAreNotFound(t *testing.T) {
	handler := testHandler(t)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		rr := serve(handler, method, "/v1/admin/upstreams", nil, withHeader("Authorization", "Bearer "+testAdminToken))
		if rr.Code != http.StatusNotFound || decodeErrorResponse(t, rr).Code != errorNotFound {
			t.Fatalf("%s /v1/admin/upstreams = %d %s", method, rr.Code, rr.Body.String())
		}
	}
}

func TestProxySetsNoCORSAndRelaysUpstreamCORS(t *testing.T) {
	handler := testHandler(t)
	stubUpstream(handler, http.Header{"Access-Control-Allow-Origin": {"https://app.example"}}, "ok")
	rr := serve(handler, http.MethodGet, "/to/example.com/v1/messages", nil, withProxyToken(testAdminToken))
	if rr.Code != http.StatusOK || rr.Header().Get("Access-Control-Allow-Origin") != "https://app.example" || rr.Header().Get("Access-Control-Expose-Headers") != "" {
		t.Fatalf("proxy headers = %+v", rr.Header())
	}
	for _, path := range []string{"/healthz", avrReceiptURL("missing")} {
		if got := serve(handler, http.MethodGet, path, nil).Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("%s set Access-Control-Allow-Origin %q", path, got)
		}
	}
	if rr := serve(handler, http.MethodOptions, avrReceiptURL("req-1"), nil); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("OPTIONS receipt = %d", rr.Code)
	}
}

func TestProxyPassesUserAgentThrough(t *testing.T) {
	for name, tc := range map[string]struct {
		header string
		want   []string
	}{
		"client value": {header: "client/1.0", want: []string{"client/1.0"}},
		"none":         {},
	} {
		t.Run(name, func(t *testing.T) {
			var got []string
			var present bool
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, present = r.Header["User-Agent"]
			}))
			defer upstream.Close()
			handler := testHandler(t)
			handler.client = upstream.Client()
			opts := []requestOption{withProxyToken(testAdminToken)}
			if tc.header != "" {
				opts = append(opts, withHeader("User-Agent", tc.header))
			}
			if rr := serve(handler, http.MethodGet, upstreamRequestPath(t, upstream, "/ua"), nil, opts...); rr.Code != http.StatusOK {
				t.Fatalf("status = %d", rr.Code)
			}
			if present != (tc.want != nil) || strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("upstream User-Agent = %v (present %t), want %v", got, present, tc.want)
			}
		})
	}
}

func TestProxyErrorOmitsUpstreamQuery(t *testing.T) {
	handler := testHandler(t)
	handler.debugErrors = true
	rr := serve(handler, http.MethodGet, "/to/127.0.0.1:1/v1/messages?key=secret-query", nil, withProxyToken(testAdminToken))
	if rr.Code != http.StatusBadGateway || strings.Contains(rr.Body.String(), "secret-query") || !strings.Contains(rr.Body.String(), "https://127.0.0.1:1/v1/messages") {
		t.Fatalf("debug error = %d %s", rr.Code, rr.Body.String())
	}
}

func TestProxyRequestBodyTooLargeIncludesRequestID(t *testing.T) {
	handler := testHandler(t)
	body := io.LimitReader(repeatByteReader{}, maxRequestBodyBytes+1)
	rr := serve(handler, http.MethodPost, "/to/example.com/v1/messages", body, withProxyToken(testAdminToken))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	errBody := decodeErrorResponse(t, rr)
	requestID := rr.Header().Get("X-VeriRelay-Request-Id")
	if errBody.Code != errorRequestBodyTooLarge || errBody.RequestID == "" || errBody.RequestID != requestID {
		t.Fatalf("error body = %+v request ID header = %q", errBody, requestID)
	}
}

func TestPrivateUpstreamIPIsRejected(t *testing.T) {
	handler := testHandler(t)
	handler.options.AllowPrivateUpstreams = false
	rr := serve(handler, http.MethodGet, "/to/127.0.0.1:8443/v1/messages", nil, withProxyToken(testAdminToken))
	if rr.Code != http.StatusForbidden || decodeErrorResponse(t, rr).Code != errorUpstreamNotAllowed {
		t.Fatalf("private upstream response = %d %s", rr.Code, rr.Body.String())
	}
}

func TestUpstreamHostNamePortValidation(t *testing.T) {
	handler := testHandler(t)
	host, err := handler.upstreamHostName("example.com:8443")
	if err != nil || host != "example.com" {
		t.Fatalf("valid host:port = %q, %v", host, err)
	}
	for _, value := range []string{"example.com:abc", "example.com:0", "example.com:70000", "example.com:"} {
		if _, err := handler.upstreamHostName(value); err == nil {
			t.Errorf("upstreamHostName(%q) succeeded, want invalid port error", value)
		} else if errors.Is(err, errUpstreamNotAllowed) || errors.Is(err, errUpstreamUnresolved) {
			t.Errorf("upstreamHostName(%q) wrapped a policy error: %v", value, err)
		}
	}
}

func TestProxyUpstreamTimeoutReturnsGatewayTimeout(t *testing.T) {
	handler := testHandler(t)
	handler.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	rr := serve(handler, http.MethodGet, "/to/example.com/v1/messages", nil, withProxyToken(testAdminToken))
	if rr.Code != http.StatusGatewayTimeout || decodeErrorResponse(t, rr).Code != errorUpstreamTimeout {
		t.Fatalf("timed out request = %d %s", rr.Code, rr.Body.String())
	}
}

func TestBufferedUpstreamResponseTooLarge(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.CopyN(w, repeatByteReader{}, maxBufferedResponseBytes+1)
	}))
	defer upstream.Close()

	handler := testHandler(t)
	handler.client = upstream.Client()
	rr := serve(handler, http.MethodGet, upstreamRequestPath(t, upstream, "/v1/messages"), nil, withProxyToken(testAdminToken))
	if rr.Code != http.StatusBadGateway || decodeErrorResponse(t, rr).Code != errorUpstreamResponseTooLarge {
		t.Fatalf("oversized upstream response = %d %s", rr.Code, rr.Body.String())
	}
}

func TestProxyPreservesEncodedUpstreamPath(t *testing.T) {
	for _, tc := range []struct {
		name        string
		path        string // escaped form of the upstream path
		wantEscaped string
	}{
		{name: "plain", path: "/v1/messages", wantEscaped: "/v1/messages"},
		{name: "space", path: "/v1/a%20b", wantEscaped: "/v1/a%20b"},
		{name: "encoded slash", path: "/v1/a%2Fb", wantEscaped: "/v1/a%2Fb"},
		{name: "percent", path: "/v1/a%25b", wantEscaped: "/v1/a%25b"},
		{name: "mixed", path: "/v1/a%2Fb/c%20d", wantEscaped: "/v1/a%2Fb/c%20d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := testHandler(t)
			var gotPath string
			handler.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				gotPath = req.URL.EscapedPath()
				return stubResponse(req, nil, io.NopCloser(strings.NewReader("ok"))), nil
			})}
			rr := serve(handler, http.MethodGet, "/to/example.com"+tc.path, nil, withProxyToken(testAdminToken))
			if rr.Code != http.StatusOK {
				t.Fatalf("proxy status = %d body=%q", rr.Code, rr.Body.String())
			}
			if gotPath != tc.wantEscaped {
				t.Fatalf("upstream escaped path = %q, want %q", gotPath, tc.wantEscaped)
			}
			receipt := fetchAVRReceipt(t, handler, rr.Header().Get("X-VeriRelay-Request-Id"))
			if receipt.UpstreamPath != tc.wantEscaped {
				t.Fatalf("receipt upstream_path = %q, want %q", receipt.UpstreamPath, tc.wantEscaped)
			}
		})
	}
}

func TestProxyRawStoreBufferedResponse(t *testing.T) {
	requestBody := []byte(`{"model":"test"}`)
	responseBody := []byte(`{"ok":true}`)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(storeRequestHeader) != "" {
			t.Error("store request header was forwarded upstream")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(storeIDHeader, "spoofed")
		_, _ = w.Write(responseBody)
	}))
	defer upstream.Close()
	handler := testHandler(t)
	handler.options.RawStore = storage.NewMemoryRawStoreWithOptions(storage.MemoryRawStoreOptions{})
	handler.client = upstream.Client()
	rr := serve(handler, http.MethodPost, upstreamRequestPath(t, upstream, "/v1/messages"), bytes.NewReader(requestBody),
		withProxyToken(testAdminToken), withHeader(storeRequestHeader, "60"))
	if rr.Code != http.StatusOK || !bytes.Equal(rr.Body.Bytes(), responseBody) {
		t.Fatalf("proxy response = %d %q", rr.Code, rr.Body.Bytes())
	}
	storeID, storeKey := rr.Header().Get(storeIDHeader), rr.Header().Get(storeKeyHeader)
	if storeID == "" || storeID == "spoofed" || storeKey == "" {
		t.Fatalf("missing store headers: %+v", rr.Header())
	}
	storeRR := serve(handler, http.MethodGet, avrStorePathPrefix+storeID, nil)
	if storeRR.Code != http.StatusOK || bytes.Contains(storeRR.Body.Bytes(), requestBody) || bytes.Contains(storeRR.Body.Bytes(), responseBody) {
		t.Fatalf("store response = %d %q", storeRR.Code, storeRR.Body.Bytes())
	}
	gotRequest, gotResponse := openRawCapture(t, storeID, storeKey, storeRR.Body.Bytes())
	if !bytes.Equal(gotRequest, requestBody) || !bytes.Equal(gotResponse, responseBody) {
		t.Fatalf("decrypted exchange = request %q response %q", gotRequest, gotResponse)
	}
}

func TestEncryptRawExchangeProducesARawCapture(t *testing.T) {
	plan := rawStorePlan{enabled: true, storeID: "store-1", key: bytes.Repeat([]byte{2}, avr1.RawCaptureKeySize)}
	data, err := encryptRawExchange(plan, []byte("request"), nil)
	if err != nil {
		t.Fatal(err)
	}
	gotRequest, gotResponse := openRawCapture(t, plan.storeID, base64.RawURLEncoding.EncodeToString(plan.key), data)
	if string(gotRequest) != "request" || len(gotResponse) != 0 {
		t.Fatalf("capture = %q / %q", gotRequest, gotResponse)
	}
}

func TestProxyRawStoreFailureAndInvalidHeaderFailOpen(t *testing.T) {
	for _, value := range []string{"invalid", "60"} {
		t.Run(value, func(t *testing.T) {
			handler := testHandler(t)
			handler.options.RawStore = failingRawStore{}
			stubUpstream(handler, nil, "ok")
			rr := serve(handler, http.MethodPost, "/to/example.com/messages", nil, withProxyToken(testAdminToken), withHeader(storeRequestHeader, value))
			if rr.Code != http.StatusOK || rr.Body.String() != "ok" || rr.Header().Get(storeIDHeader) != "" || rr.Header().Get(storeKeyHeader) != "" {
				t.Fatalf("fail-open response = %d %q headers=%+v", rr.Code, rr.Body.String(), rr.Header())
			}
			_ = fetchAVRReceipt(t, handler, rr.Header().Get("X-VeriRelay-Request-Id"))
		})
	}
}

func TestRawStoreTTLAndStreamingFailure(t *testing.T) {
	handler := testHandler(t)
	handler.options.RawStore = storage.NewMemoryRawStoreWithOptions(storage.MemoryRawStoreOptions{})
	for value, want := range map[string]time.Duration{"1": time.Second, "999999": 999999 * time.Second} {
		if plan := handler.newRawStorePlan(value, "request"); !plan.enabled || plan.ttl != want {
			t.Fatalf("ttl %s = %v enabled=%v, want %v", value, plan.ttl, plan.enabled, want)
		}
	}
	for _, value := range []string{"0", "-5", "abc"} {
		if plan := handler.newRawStorePlan(value, "request"); plan.enabled {
			t.Fatalf("ttl %s enabled the raw store", value)
		}
	}
	handler.options.RawStoreMaxTTL = time.Minute
	if plan := handler.newRawStorePlan("999999", "request"); !plan.enabled || plan.ttl != time.Minute {
		t.Fatalf("capped ttl = %v enabled=%v", plan.ttl, plan.enabled)
	}
	handler.options.RawStoreMaxTTL = 0
	handler.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return stubResponse(req, http.Header{"Content-Type": {"text/event-stream"}}, &partialErrorReadCloser{data: []byte("data: partial\n\n"), err: errors.New("upstream failed")}), nil
	})}
	rr := serve(handler, http.MethodGet, "/to/example.com/stream", nil, withProxyToken(testAdminToken), withHeader(storeRequestHeader, "60"))
	storeID := rr.Header().Get(storeIDHeader)
	if storeID == "" {
		t.Fatal("streaming response did not send store headers")
	}
	lookup := serve(handler, http.MethodGet, avrStorePathPrefix+storeID, nil)
	if lookup.Code != http.StatusNotFound {
		t.Fatalf("interrupted stream store = %d %q", lookup.Code, lookup.Body.String())
	}
}

func TestProxyForwardsJSONAndStoresSignedAVRReceipt(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("authorization was not forwarded")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Upstream-Test", "present")
		w.Header().Set("X-VeriRelay-Proof-URL", "spoofed")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	handler := testHandler(t)
	handler.client = upstream.Client()
	rr := serve(handler, http.MethodPost, upstreamRequestPath(t, upstream, "/v1/messages"), strings.NewReader(`{"model":"test"}`),
		withProxyToken(testAdminToken), withHeader("Authorization", "Bearer test"))

	if rr.Code != http.StatusOK || rr.Body.String() != `{"ok":true}` {
		t.Fatalf("proxy response = %d %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("X-Upstream-Test") != "present" || rr.Header().Get("X-VeriRelay-Proof-URL") != "" {
		t.Fatalf("unexpected response headers: %+v", rr.Header())
	}
	requestID := rr.Header().Get("X-VeriRelay-Request-Id")
	receipt := fetchAVRReceipt(t, handler, requestID)
	if err := avr1.VerifySignedReceiptV1(receipt); err != nil {
		t.Fatalf("receipt did not verify: %v", err)
	}
	if receipt.Version != avr1.AVRVersion || receipt.RequestBodySHA256 != avr1.SHA256Hex([]byte(`{"model":"test"}`)) || receipt.ResponseBodySHA256 != avr1.SHA256Hex([]byte(`{"ok":true}`)) {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	certificate := upstream.Certificate()
	if receipt.UpstreamTLS.SPKISHA256 != avr1.SHA256Hex(certificate.RawSubjectPublicKeyInfo) {
		t.Fatalf("receipt spki = %s", receipt.UpstreamTLS.SPKISHA256)
	}
}

func TestSSEUsesAVRReceiptAfterCompleteEOF(t *testing.T) {
	body := []byte(": keepalive\r\nevent: message\r\ndata: {\"n\":1}\r\n\r\ndata: tail\n\n")
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-VeriRelay-Receipt-Status", "spoofed")
				w.WriteHeader(status)
				_, _ = w.Write(body)
			}))
			defer upstream.Close()
			handler := testHandler(t)
			handler.client = upstream.Client()
			proxyServer := httptest.NewServer(handler)
			defer proxyServer.Close()

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, proxyServer.URL+upstreamRequestPath(t, upstream, "/v1/messages"), nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(proxyTokenHeader, testAdminToken)
			resp, err := proxyServer.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || resp.StatusCode != status || !bytes.Equal(got, body) {
				t.Fatalf("response = status %d body %q err %v", resp.StatusCode, got, err)
			}
			if resp.Header.Get("X-VeriRelay-Receipt-Status") != "" {
				t.Fatalf("unexpected receipt headers: %+v", resp.Header)
			}
			receipt := fetchAVRReceipt(t, handler, resp.Header.Get("X-VeriRelay-Request-Id"))
			if receipt.ResponseStatus != status || receipt.ResponseBodySHA256 != avr1.SHA256Hex(body) || avr1.VerifySignedReceiptV1(receipt) != nil {
				t.Fatalf("unexpected SSE receipt: %+v", receipt)
			}
		})
	}
}

func TestSSEHasNoReceiptBeforeEOF(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-release
		_, _ = w.Write([]byte("data: complete\n\n"))
	}))
	defer upstream.Close()
	handler := testHandler(t)
	handler.client = upstream.Client()
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, proxyServer.URL+upstreamRequestPath(t, upstream, "/v1/messages"), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(proxyTokenHeader, testAdminToken)
	resp, err := proxyServer.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream did not flush headers")
	}
	requestID := resp.Header.Get("X-VeriRelay-Request-Id")
	requireReceiptNotFound(t, handler, requestID)
	close(release)
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	_ = fetchAVRReceipt(t, handler, requestID)
}

func TestSSEFailuresDoNotPublishReceipt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   io.ReadCloser
		writer *failingResponseWriter
		cancel bool
	}{
		{name: "upstream read error", body: &partialErrorReadCloser{data: []byte("data: partial\n\n"), err: errors.New("upstream failed")}, writer: &failingResponseWriter{}},
		{name: "write error", body: io.NopCloser(strings.NewReader("data: one\n\n")), writer: &failingResponseWriter{writeErr: errors.New("client gone")}},
		{name: "short write", body: io.NopCloser(strings.NewReader("data: one\n\n")), writer: &failingResponseWriter{limit: 2}},
		{name: "context cancelled", body: &countingReadCloser{data: []byte("data: one\n\n")}, writer: &failingResponseWriter{}, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := testHandler(t)
			handler.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return stubResponse(req, http.Header{"Content-Type": {"text/event-stream"}}, tc.body), nil
			})}
			ctx := context.Background()
			if tc.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/to/example.com/stream", nil)
			request.Header.Set(proxyTokenHeader, testAdminToken)
			handler.ServeHTTP(tc.writer, request)
			requireReceiptNotFound(t, handler, tc.writer.Header().Get("X-VeriRelay-Request-Id"))
		})
	}
}

func TestSSEStoreFailureDoesNotPublishReceipt(t *testing.T) {
	handler := testHandler(t)
	handler.options.ReceiptStore = failingReceiptStore{}
	stubUpstream(handler, http.Header{"Content-Type": {"text/event-stream"}}, "data: one\n\n")
	rr := serve(handler, http.MethodGet, "/to/example.com/stream", nil, withProxyToken(testAdminToken))
	if rr.Code != http.StatusOK || rr.Body.String() != "data: one\n\n" {
		t.Fatalf("response changed after save failure: %d %q", rr.Code, rr.Body.String())
	}
	requireReceiptNotFound(t, handler, rr.Header().Get("X-VeriRelay-Request-Id"))
}

func TestAVRReceiptNotFoundForUnknownOrProxyFailure(t *testing.T) {
	handler := NewHandler(true, Options{AllowPrivateUpstreams: true, AdminToken: testAdminToken})
	rr := serve(handler, http.MethodGet, "/to/127.0.0.1:1/v1/messages", nil, withProxyToken(testAdminToken))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("proxy failure status = %d", rr.Code)
	}
	requireReceiptNotFound(t, handler, rr.Header().Get("X-VeriRelay-Request-Id"))
	requireReceiptNotFound(t, testHandler(t), "missing")
}

func TestHealthAndReadyEndpoints(t *testing.T) {
	handler := testHandler(t)
	for _, path := range []string{"/healthz", "/readyz"} {
		rr := serve(handler, http.MethodGet, path, nil)
		if rr.Code != http.StatusOK || rr.Body.String() != "ok\n" {
			t.Fatalf("%s = %d %q", path, rr.Code, rr.Body.String())
		}
	}

	handler.options.ReceiptStore = notReadyReceiptStore{}
	rr := serve(handler, http.MethodGet, "/readyz", nil)
	if rr.Code != http.StatusServiceUnavailable || rr.Body.String() != "not_ready\n" {
		t.Fatalf("not ready = %d %q", rr.Code, rr.Body.String())
	}

	handler.options.ReceiptStore = storage.NewMemoryReceiptStore()
	handler.options.AttestationStore = notReadyAttestationStore{}
	if err := handler.Ready(context.Background()); err == nil {
		t.Fatal("Ready succeeded with a failing attestation store")
	}
	rr = serve(handler, http.MethodGet, "/readyz", nil)
	if rr.Code != http.StatusServiceUnavailable || rr.Body.String() != "not_ready\n" {
		t.Fatalf("attestation not ready = %d %q", rr.Code, rr.Body.String())
	}

	rr = serve(handler, http.MethodPost, "/readyz", nil)
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("ready method error = %d allow=%q", rr.Code, rr.Header().Get("Allow"))
	}
}

func TestAVRReceiptStoreErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		store      storage.ReceiptStore
		wantStatus int
		wantCode   errorCode
	}{
		{name: "unavailable", store: unavailableReceiptStore{}, wantStatus: http.StatusServiceUnavailable, wantCode: errorReceiptStoreUnavailable},
		{name: "corrupt", store: corruptReceiptStore{}, wantStatus: http.StatusInternalServerError, wantCode: errorReceiptStoreCorrupt},
		{name: "invalid returned receipt", store: invalidReceiptStore{}, wantStatus: http.StatusInternalServerError, wantCode: errorReceiptStoreCorrupt},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := testHandler(t)
			handler.options.ReceiptStore = tc.store
			rr := serve(handler, http.MethodGet, avrReceiptURL("req-1"), nil)
			if rr.Code != tc.wantStatus || decodeErrorResponse(t, rr).Code != tc.wantCode {
				t.Fatalf("lookup = %d %q", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestAVREvidenceCorruptAttestationStoreReturnsInternalServerError(t *testing.T) {
	handler := testHandler(t)
	stubUpstream(handler, http.Header{"Content-Type": {"application/json"}}, `{"ok":true}`)
	proxyResponse := serve(handler, http.MethodGet, "/to/example.com/v1/messages", nil, withProxyToken(testAdminToken))
	requestID := proxyResponse.Header().Get("X-VeriRelay-Request-Id")
	if proxyResponse.Code != http.StatusOK || requestID == "" {
		t.Fatalf("proxy response = %d %s", proxyResponse.Code, proxyResponse.Body.String())
	}
	_ = fetchAVRReceipt(t, handler, requestID)
	handler.options.AttestationStore = corruptAttestationStore{}
	rr := serve(handler, http.MethodGet, avrEvidencePathPrefix+requestID, nil)
	if rr.Code != http.StatusInternalServerError || decodeErrorResponse(t, rr).Code != errorAttestationStoreCorrupt {
		t.Fatalf("evidence response = %d %s", rr.Code, rr.Body.String())
	}
}

func TestAVRRawStoreCorruptReturnsInternalServerError(t *testing.T) {
	handler := testHandler(t)
	handler.options.RawStore = corruptRawStore{}
	rr := serve(handler, http.MethodGet, avrStorePathPrefix+"store-id", nil)
	if rr.Code != http.StatusInternalServerError || decodeErrorResponse(t, rr).Code != errorStoreCorrupt {
		t.Fatalf("raw store response = %d %s", rr.Code, rr.Body.String())
	}
}

func TestAVRReceiptMethodNotAllowedSetsAllow(t *testing.T) {
	rr := serve(testHandler(t), http.MethodPost, avrReceiptURL("req-1"), nil)
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != http.MethodGet || decodeErrorResponse(t, rr).Code != errorMethodNotAllowed {
		t.Fatalf("receipt method error = %d allow=%q body=%s", rr.Code, rr.Header().Get("Allow"), rr.Body.String())
	}
}

func TestAVREvidenceReturnsReceiptAndArchivedAttestation(t *testing.T) {
	handler := testHandler(t)
	handler.options.AttestationManager = attestation.NewManagerWithOptions(attestation.FakeProvider{}, attestation.ManagerOptions{DisclosureReader: testDisclosureReader{}})
	publicKey, err := avr1.DecodeEd25519PublicKeyBase64URL(handler.options.ReceiptSigner.PublicKeyBase64URL())
	if err != nil {
		t.Fatal(err)
	}
	zeroNonce := make([]byte, avr1.AVRNonceBytes)
	archived, err := handler.options.AttestationManager.Attestation(context.Background(), attestation.Request{Nonce: zeroNonce, ReceiptPublicKey: publicKey, KeyID: handler.options.ReceiptSigner.KeyID()})
	if err != nil {
		t.Fatal(err)
	}
	handler.options.ArchivedAttestation = &archived
	handler.options.ArchivedAttestationTTL = time.Hour
	if err := handler.options.AttestationStore.Save(context.Background(), archived.KeyID, archived, time.Hour); err != nil {
		t.Fatal(err)
	}

	stubUpstream(handler, http.Header{"Content-Type": {"application/json"}}, `{"ok":true}`)
	proxyRR := serve(handler, http.MethodGet, "/to/example.com/evidence", nil, withProxyToken(testAdminToken))
	requestID := proxyRR.Header().Get("X-VeriRelay-Request-Id")
	_ = fetchAVRReceipt(t, handler, requestID)

	rr := serve(handler, http.MethodGet, avrEvidencePathPrefix+requestID, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("evidence status = %d body=%s", rr.Code, rr.Body.String())
	}
	bundle, err := avr1.DecodeReceiptEvidenceBundle(rr.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Version != avr1.AVRVersion || bundle.Receipt.RequestID != requestID || bundle.Attestation == nil || *bundle.Attestation != archived {
		t.Fatalf("unexpected evidence: %+v", bundle)
	}
	if bundle.Receipt.KeyID != bundle.Attestation.KeyID || bundle.Receipt.ServicePublicKey != bundle.Attestation.ReceiptSigningPublicKey {
		t.Fatalf("receipt/attestation mismatch: receipt=%+v attestation=%+v", bundle.Receipt, bundle.Attestation)
	}
	if err := avr1.VerifySignedReceiptV1(bundle.Receipt); err != nil {
		t.Fatal(err)
	}

	handler.options.AttestationStore = storage.NewMemoryAttestationStore()
	rr = serve(handler, http.MethodGet, avrEvidencePathPrefix+requestID, nil)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"attestation":null`) {
		t.Fatalf("evidence without archive = %d %s", rr.Code, rr.Body.String())
	}
}

type failingReportProvider struct {
	err error
}

func (p failingReportProvider) Quote(context.Context, attestation.QuoteRequest) (attestation.Quote, error) {
	return attestation.Quote{}, p.err
}

func TestAttestationReportFailureCode(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   errorCode
	}{
		{"collection failure", errors.New("report collection failed"), http.StatusBadGateway, errorAttestationReportFailed},
		{"provider unavailable", attestation.ErrQuoteProviderUnavailable, http.StatusServiceUnavailable, errorAttestationUnavailable},
		{"unsupported signer", fmt.Errorf("quote provider: %w", attestation.ErrUnsupportedReportSigner), http.StatusInternalServerError, errorAttestationReportUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := testHandler(t)
			handler.options.AttestationManager = attestation.NewManagerWithOptions(failingReportProvider{err: test.err}, attestation.ManagerOptions{DisclosureReader: testDisclosureReader{}})
			nonce := base64.RawURLEncoding.EncodeToString([]byte("abcdef0123456789abcdef0123456789"))
			rr := serve(handler, http.MethodGet, attestationPath+"?nonce="+nonce, nil)
			if rr.Code != test.status || decodeErrorResponse(t, rr).Code != test.code {
				t.Fatalf("attestation report failure = %d %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestAttestationFailsClosedWhenDisclosureFails(t *testing.T) {
	nonce := base64.RawURLEncoding.EncodeToString([]byte("abcdef0123456789abcdef0123456789"))
	for _, cause := range []error{
		attestation.ErrDisclosureUnavailable,
		attestation.ErrInvalidManifest,
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			handler := testHandler(t)
			handler.options.AttestationManager = attestation.NewManagerWithOptions(attestation.FakeProvider{}, attestation.ManagerOptions{DisclosureReader: failingDisclosureReader{err: cause}})
			rr := serve(handler, http.MethodGet, attestationPath+"?nonce="+nonce, nil)
			if rr.Code != http.StatusInternalServerError || decodeErrorResponse(t, rr).Code != errorAttestationDisclosureFailed {
				t.Fatalf("attestation disclosure failure = %d %s", rr.Code, rr.Body.String())
			}
		})
	}

	debugHandler := testHandler(t)
	debugHandler.debugErrors = true
	debugHandler.options.AttestationManager = attestation.NewManagerWithOptions(attestation.FakeProvider{}, attestation.ManagerOptions{DisclosureReader: failingDisclosureReader{err: attestation.ErrInvalidManifest}})
	debugRR := serve(debugHandler, http.MethodGet, attestationPath+"?nonce="+nonce, nil)
	errBody := decodeErrorResponse(t, debugRR)
	if debugRR.Code != http.StatusInternalServerError || errBody.Code != errorAttestationDisclosureFailed || errBody.Message != errorSpecs[errorAttestationDisclosureFailed].message+": "+attestation.ErrInvalidManifest.Error() {
		t.Fatalf("debug attestation disclosure failure = %d %s", debugRR.Code, debugRR.Body.String())
	}
	if strings.Contains(debugRR.Body.String(), "\"detail\"") {
		t.Fatalf("debug response has a detail field: %s", debugRR.Body.String())
	}
}

func TestAttestationBindsSSEReceiptKey(t *testing.T) {
	handler := testHandler(t)
	bootstrap := []byte(`{"version":"veriprov-bootstrap/1"}`)
	handler.options.AttestationManager = attestation.NewManagerWithOptions(attestation.FakeProvider{}, attestation.ManagerOptions{DisclosureReader: testDisclosureReader{}, Bootstrap: bootstrap})
	nonce := []byte("abcdef0123456789abcdef0123456789")
	attestationRR := serve(handler, http.MethodGet, attestationPath+"?nonce="+base64.RawURLEncoding.EncodeToString(nonce), nil)
	if attestationRR.Code != http.StatusOK || attestationRR.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("attestation status = %d headers %+v", attestationRR.Code, attestationRR.Header())
	}
	response, err := avr1.DecodeAttestationResponse(attestationRR.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if response.Source.VeriRelay.Repository != testVeriRelayRepository || response.Source.VeriRelay.Commit != testVeriRelayCommit || response.LaunchProfile.KernelAppend != "console=ttyS0 panic=1" {
		t.Fatalf("unexpected attestation disclosure: source=%+v launch_profile=%+v", response.Source, response.LaunchProfile)
	}
	if response.Bootstrap != base64.RawURLEncoding.EncodeToString(bootstrap) {
		t.Fatalf("bootstrap = %q", response.Bootstrap)
	}

	stubUpstream(handler, http.Header{"Content-Type": {"text/event-stream"}}, "data: one\n\n")
	rr := serve(handler, http.MethodGet, "/to/example.com/stream", nil, withProxyToken(testAdminToken))
	receipt := fetchAVRReceipt(t, handler, rr.Header().Get("X-VeriRelay-Request-Id"))
	if receipt.ServicePublicKey != response.ReceiptSigningPublicKey || receipt.KeyID != response.KeyID {
		t.Fatalf("attestation does not bind SSE receipt: response=%+v receipt=%+v", response, receipt)
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"source", "launch", "launch_profile", "manifest", "bootstrap"} {
		if bytes.Contains(receiptJSON, []byte(field)) {
			t.Fatalf("receipt included attestation disclosure field %q: %s", field, receiptJSON)
		}
	}
}

const (
	testVeriRelayRepository = "https://github.com/verirelay/verirelay"
	testVeriForgeRepository = "https://github.com/verirelay/veriforge"
	testVeriRelayCommit     = "0123456789abcdef0123456789abcdef01234567"
	testVeriForgeCommit     = "89abcdef0123456789abcdef0123456789abcdef"
)

type failingRawStore struct{}

func (failingRawStore) Save(context.Context, string, []byte, time.Duration) error {
	return errors.New("raw persistence failed")
}

func (failingRawStore) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, nil
}

type corruptRawStore struct{}

func (corruptRawStore) Save(context.Context, string, []byte, time.Duration) error { return nil }

func (corruptRawStore) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, storage.ErrRawStoreCorrupt
}

type corruptAttestationStore struct{}

func (corruptAttestationStore) Save(context.Context, string, avr1.AttestationResponse, time.Duration) error {
	return nil
}

func (corruptAttestationStore) Get(context.Context, string) (avr1.AttestationResponse, bool, error) {
	return avr1.AttestationResponse{}, false, storage.ErrCorruptAttestationRecord
}

type repeatByteReader struct{}

func (repeatByteReader) Read(buffer []byte) (int, error) {
	for i := range buffer {
		buffer[i] = 'x'
	}
	return len(buffer), nil
}

func openRawCapture(t *testing.T, storeID string, encodedKey string, data []byte) ([]byte, []byte) {
	t.Helper()
	key, err := base64.RawURLEncoding.DecodeString(encodedKey)
	if err != nil {
		t.Fatal(err)
	}
	capture, err := avr1.DecodeRawCapture(data)
	if err != nil {
		t.Fatal(err)
	}
	requestBody, responseBody, err := avr1.OpenRawCapture(key, storeID, capture)
	if err != nil {
		t.Fatal(err)
	}
	return requestBody, responseBody
}

type failingReceiptStore struct{}

func (failingReceiptStore) Save(context.Context, string, avr1.SignedReceiptV1) error {
	return errors.New("persistence failed")
}

func (failingReceiptStore) Get(context.Context, string) (avr1.SignedReceiptV1, bool, error) {
	return avr1.SignedReceiptV1{}, false, nil
}

type notReadyReceiptStore struct{ failingReceiptStore }

func (notReadyReceiptStore) Ready(context.Context) error { return storage.ErrStoreUnavailable }

type notReadyAttestationStore struct{}

func (notReadyAttestationStore) Save(context.Context, string, avr1.AttestationResponse, time.Duration) error {
	return nil
}

func (notReadyAttestationStore) Get(context.Context, string) (avr1.AttestationResponse, bool, error) {
	return avr1.AttestationResponse{}, false, nil
}

func (notReadyAttestationStore) Ready(context.Context) error {
	return storage.ErrAttestationStoreUnavailable
}

type unavailableReceiptStore struct{}

func (unavailableReceiptStore) Save(context.Context, string, avr1.SignedReceiptV1) error {
	return nil
}

func (unavailableReceiptStore) Get(context.Context, string) (avr1.SignedReceiptV1, bool, error) {
	return avr1.SignedReceiptV1{}, false, storage.ErrStoreUnavailable
}

type corruptReceiptStore struct{}

func (corruptReceiptStore) Save(context.Context, string, avr1.SignedReceiptV1) error { return nil }
func (corruptReceiptStore) Get(context.Context, string) (avr1.SignedReceiptV1, bool, error) {
	return avr1.SignedReceiptV1{}, false, storage.ErrCorruptRecord
}

type invalidReceiptStore struct{}

func (invalidReceiptStore) Save(context.Context, string, avr1.SignedReceiptV1) error { return nil }
func (invalidReceiptStore) Get(context.Context, string) (avr1.SignedReceiptV1, bool, error) {
	return avr1.SignedReceiptV1{RequestID: "req-1"}, true, nil
}

type countingReadCloser struct {
	data []byte
}

func (r *countingReadCloser) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}
func (*countingReadCloser) Close() error { return nil }

type failingResponseWriter struct {
	header   http.Header
	writeErr error
	limit    int
}

func (w *failingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (*failingResponseWriter) WriteHeader(int) {}
func (w *failingResponseWriter) Write(data []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	if w.limit > 0 && w.limit < len(data) {
		return w.limit, nil
	}
	return len(data), nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type partialErrorReadCloser struct {
	data []byte
	err  error
	read bool
}

func (r *partialErrorReadCloser) Read(p []byte) (int, error) {
	if !r.read {
		r.read = true
		return copy(p, r.data), nil
	}
	return 0, r.err
}
func (*partialErrorReadCloser) Close() error { return nil }

type testDisclosureReader struct{}

func (testDisclosureReader) ReadManifest(context.Context) (attestation.ManifestDisclosure, error) {
	return attestation.ManifestDisclosure{
		Source: avr1.AttestationSource{
			VeriRelay: avr1.AttestationSourceRef{Repository: testVeriRelayRepository, Commit: testVeriRelayCommit},
			VeriForge: avr1.AttestationSourceRef{Repository: testVeriForgeRepository, Commit: testVeriForgeCommit},
		},
		LaunchProfile: avr1.AttestationLaunchProfile{
			Version: guest.LaunchProfileVersion, MeasurementProfile: guest.MeasurementProfile,
			VCPUType: guest.VCPUType, GuestFeatures: guest.GuestFeatures, KernelHashes: guest.KernelHashes, KernelAppend: "console=ttyS0 panic=1",
		},
	}, nil
}

type failingDisclosureReader struct{ err error }

func (r failingDisclosureReader) ReadManifest(context.Context) (attestation.ManifestDisclosure, error) {
	return attestation.ManifestDisclosure{}, r.err
}

func avrReceiptURL(requestID string) string { return avrReceiptPathPrefix + requestID }

// fetchAVRReceipt polls because a streamed response can reach the client
// before its Receipt is published.
func fetchAVRReceipt(t *testing.T, handler *Handler, requestID string) avr1.SignedReceiptV1 {
	t.Helper()
	var rr *httptest.ResponseRecorder
	for range 100 {
		rr = serve(handler, http.MethodGet, avrReceiptURL(requestID), nil)
		if rr.Code == http.StatusOK {
			receipt, err := avr1.VerifySignedReceiptJSONV1(bytes.TrimSuffix(rr.Body.Bytes(), []byte("\n")))
			if err != nil {
				t.Fatal(err)
			}
			return receipt
		}
		if rr.Code != http.StatusNotFound {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("receipt status = %d body=%s", rr.Code, rr.Body.String())
	return avr1.SignedReceiptV1{}
}

func requireReceiptNotFound(t *testing.T, handler *Handler, requestID string) {
	t.Helper()
	rr := serve(handler, http.MethodGet, avrReceiptURL(requestID), nil)
	if rr.Code != http.StatusNotFound || decodeErrorResponse(t, rr).Code != errorReceiptNotFound {
		t.Fatalf("unexpected receipt lookup: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func decodeErrorResponse(t *testing.T, response *httptest.ResponseRecorder) errorResponseDetail {
	t.Helper()
	var body errorResponseBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, response.Body.String())
	}
	return body.Error
}

func upstreamRequestPath(t *testing.T, upstream *httptest.Server, path string) string {
	t.Helper()
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	return "/to/" + upstreamURL.Host + path
}

func testHandler(t *testing.T) *Handler {
	t.Helper()
	signer, err := avr1.NewReceiptSignerV1(ed25519.NewKeyFromSeed([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	return NewHandler(false, Options{AllowPrivateUpstreams: true, AdminToken: testAdminToken, ReceiptSigner: signer, ReceiptStore: storage.NewMemoryReceiptStore()})
}
