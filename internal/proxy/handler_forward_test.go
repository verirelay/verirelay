package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/verirelay/protocol/avr1"
)

// rawTLSUpstream serves one request per TLS 1.3 connection. It reads the
// request head, reports the request, and calls respond, which writes the
// response bytes by hand and may read the body. The connection then ends
// with a close_notify unless respond ended it.
func rawTLSUpstream(t *testing.T, respond func(c *tls.Conn, req *http.Request)) (string, *x509.CertPool, <-chan *http.Request) {
	t.Helper()
	server := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	cfg := server.TLS.Clone()
	cfg.NextProtos = []string{"http/1.1"}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	requests := make(chan *http.Request, 16)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c *tls.Conn) {
				defer func() { _ = c.Close() }()
				req, err := http.ReadRequest(bufio.NewReader(c))
				if err != nil {
					return
				}
				requests <- req
				respond(c, req)
			}(conn.(*tls.Conn))
		}
	}()
	return listener.Addr().String(), pool, requests
}

// wireHandler is testHandler with the production Upstream Transport trusting
// only pool.
func wireHandler(t *testing.T, pool *x509.CertPool) *Handler {
	t.Helper()
	handler := testHandler(t)
	handler.transport.TLSClientConfig.RootCAs = pool
	handler.client = newUpstreamClient(handler.transport)
	return handler
}

func storedReceipt(t *testing.T, handler *Handler, rr *httptest.ResponseRecorder) (avr1.SignedReceiptV1, bool) {
	t.Helper()
	receipt, ok, err := handler.options.ReceiptStore.Get(context.Background(), rr.Header().Get(requestIDHeader))
	if err != nil {
		t.Fatal(err)
	}
	return receipt, ok
}

// finWithoutCloseNotify ends the TCP connection at a TLS record boundary
// without a close_notify alert, as anyone on the network path can.
func finWithoutCloseNotify(c *tls.Conn) {
	_ = c.NetConn().(*net.TCPConn).CloseWrite()
}

func TestReceiptNeedsAFramedResponseBody(t *testing.T) {
	for _, tc := range []struct {
		name, method, head, body string
		cut                      bool
		wantReceipt              bool
	}{
		{name: "content-length", method: http.MethodGet, head: "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n", body: "ok", wantReceipt: true},
		{name: "chunked", method: http.MethodGet, head: "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n", body: "2\r\nok\r\n0\r\n\r\n", wantReceipt: true},
		{name: "head without length", method: http.MethodHead, head: "HTTP/1.1 200 OK\r\n", wantReceipt: true},
		{name: "204 without length", method: http.MethodGet, head: "HTTP/1.1 204 No Content\r\n", wantReceipt: true},
		{name: "304 without length", method: http.MethodGet, head: "HTTP/1.1 304 Not Modified\r\n", wantReceipt: true},
		{name: "close-delimited", method: http.MethodGet, head: "HTTP/1.1 200 OK\r\nConnection: close\r\n", body: "complete\n"},
		{name: "http/1.0 close-delimited", method: http.MethodGet, head: "HTTP/1.0 200 OK\r\n", body: "complete\n"},
		{name: "close-delimited cut", method: http.MethodGet, head: "HTTP/1.1 200 OK\r\nConnection: close\r\n", body: "status: transfer APPROVED\n", cut: true},
		{name: "close-delimited sse cut", method: http.MethodGet, head: "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\n", body: "data: Yes, this command is safe\n\n", cut: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr, pool, _ := rawTLSUpstream(t, func(c *tls.Conn, _ *http.Request) {
				_, _ = io.WriteString(c, tc.head+"\r\n"+tc.body)
				if tc.cut {
					finWithoutCloseNotify(c)
				}
			})
			handler := wireHandler(t, pool)
			proxyServer := httptest.NewServer(handler)
			defer proxyServer.Close()
			req, err := http.NewRequestWithContext(t.Context(), tc.method, proxyServer.URL+"/to/"+addr+"/v1/x", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set(proxyTokenHeader, testAdminToken)
			resp, err := proxyServer.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil || resp.StatusCode >= http.StatusBadRequest || resp.Header.Get(requestIDHeader) == "" {
				t.Fatalf("response = %d %q %v, want the Upstream response relayed", resp.StatusCode, body, err)
			}
			if !tc.wantReceipt && string(body) != tc.body {
				t.Fatalf("relayed body = %q, want %q", body, tc.body)
			}
			requestID := resp.Header.Get(requestIDHeader)
			if tc.wantReceipt {
				_ = fetchAVRReceipt(t, handler, requestID)
			} else {
				requireReceiptNotFound(t, handler, requestID)
			}
		})
	}
}

func TestUpstreamPathIsTheRequestTargetSent(t *testing.T) {
	addr, pool, requests := rawTLSUpstream(t, func(c *tls.Conn, _ *http.Request) {
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
	})
	handler := wireHandler(t, pool)
	for _, suffix := range []string{
		"/v1/prices?symbol=ETH&symbol=BTC",
		"/v1/a%2Fb/%7E?q=%E2%82%AC&r=a+b",
		`/v1/a?q='"<>{}|\^` + "`",
		"/v1/a?",
		"/v1/a\xffb",
	} {
		rr := serve(handler, http.MethodGet, "/to/"+addr+suffix, nil, withProxyToken(testAdminToken))
		wire := <-requests
		receipt, ok := storedReceipt(t, handler, rr)
		if !ok {
			t.Fatalf("%q: no receipt: %d %s", suffix, rr.Code, rr.Body.String())
		}
		// A Verifier reads the Receipt as JSON.
		data, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		served, err := avr1.VerifySignedReceiptJSONV1(data)
		if err != nil {
			t.Fatal(err)
		}
		if served.UpstreamPath != wire.RequestURI {
			t.Fatalf("%q: upstream_path = %q, request-target sent = %q", suffix, served.UpstreamPath, wire.RequestURI)
		}
	}
}

func TestQueryThatCannotBeSignedAsSentIsRejected(t *testing.T) {
	handler := testHandler(t)
	handler.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		t.Errorf("forwarded %s", req.URL.RequestURI())
		return stubResponse(req, nil, io.NopCloser(strings.NewReader("ok"))), nil
	})}
	for _, query := range []string{"symbol=ETH#&symbol=BTC", "#", "q=\xff\xfe", "q=\xc0\xc1", "q=\xef\xbf\xbd"} {
		rr := serve(handler, http.MethodGet, "/to/example.com/v1/prices?"+query, nil, withProxyToken(testAdminToken))
		if rr.Code != http.StatusBadRequest || decodeErrorResponse(t, rr).Code != errorUpstreamRouteInvalid {
			t.Fatalf("query %q: %d %s", query, rr.Code, rr.Body.String())
		}
		requireReceiptNotFound(t, handler, rr.Header().Get(requestIDHeader))
	}
}

// TestDialCheckBindsTheReceiptHost drives the production client (private
// Upstreams off, pre-resolved IPs) with hosts the Transport would dial,
// verify or send as SNI and Host differently from the host the Receipt
// names. Only a host that names itself the same way may connect.
func TestDialCheckBindsTheReceiptHost(t *testing.T) {
	addr, pool, _ := rawTLSUpstream(t, func(c *tls.Conn, _ *http.Request) {
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
	})
	_, port, _ := net.SplitHostPort(addr)
	handler := wireHandler(t, pool)
	handler.options.AllowPrivateUpstreams = false
	for _, tc := range []struct {
		host      string
		wantDials bool
	}{
		{"example.com", true}, // httptest certificates name example.com
		{"EXAMPLE.com", true},
		{"example.com.", false},
		{"ⓔxample.com", false},
		{"ｅxample.com", false},
	} {
		authority := net.JoinHostPort(tc.host, port)
		route := proxyRoute{
			upstream:    &url.URL{Scheme: "https", Host: authority, Path: "/"},
			target:      url.URL{Scheme: "https", Host: authority, Path: "/x"},
			resolvedIPs: []netip.Addr{netip.MustParseAddr("127.0.0.1")},
		}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/to/"+authority+"/x", nil)
		out, failure := handler.buildUpstreamRequest(req, route, nil, "request-1")
		if failure != nil {
			t.Fatalf("%s: build: %s", tc.host, failure.detail)
		}
		resp, err := handler.clientForRoute(route).Do(out)
		if err == nil {
			_ = resp.Body.Close()
		}
		if dialed := err == nil; dialed != tc.wantDials {
			t.Errorf("host %q: connected = %t (%v), want %t", tc.host, dialed, err, tc.wantDials)
		}
	}
}

func TestRoutingHeadersAndExpectAreNotForwarded(t *testing.T) {
	dropped := []string{
		"Expect", "Forwarded", "X-Forwarded-Host", "X-Forwarded-Server", "X-Host",
		"X-HTTP-Method", "X-HTTP-Method-Override", "X-Method-Override", "X-Original-URL", "X-Rewrite-URL",
		"x-http-method-override", "X_Forwarded_Host", "X_Original_URL",
	}
	addr, pool, requests := rawTLSUpstream(t, func(c *tls.Conn, _ *http.Request) {
		_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
	})
	handler := wireHandler(t, pool)
	opts := []requestOption{withProxyToken(testAdminToken), withHeader("X-Forwarded-For", "203.0.113.7")}
	for _, key := range dropped {
		value := "/admin"
		if key == "Expect" {
			value = "100-continue"
		}
		opts = append(opts, func(r *http.Request) { r.Header[key] = []string{value} })
	}
	if rr := serve(handler, http.MethodPost, "/to/"+addr+"/v1/x", strings.NewReader("body"), opts...); rr.Code != http.StatusOK {
		t.Fatalf("status = %d %s", rr.Code, rr.Body.String())
	}
	wire := <-requests
	for key := range wire.Header {
		if isUnforwardedRequestHeader(key) {
			t.Errorf("Upstream received %s: %q", key, wire.Header[key])
		}
	}
	if got := wire.Header.Get("X-Forwarded-For"); got != "203.0.113.7" {
		t.Fatalf("X-Forwarded-For = %q, want it forwarded", got)
	}
}

// TestRequestBodyIsSentBeforeAnEarlyResponse: an Upstream may answer and
// close before reading the body. The body the Receipt names must still have
// been sent.
func TestRequestBodyIsSentBeforeAnEarlyResponse(t *testing.T) {
	received := make(chan []byte, 1)
	addr, pool, _ := rawTLSUpstream(t, func(c *tls.Conn, req *http.Request) {
		_, _ = io.WriteString(c, "HTTP/1.1 401 Unauthorized\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		body, _ := io.ReadAll(req.Body)
		received <- body
	})
	handler := wireHandler(t, pool)
	body := `{"prompt":"sent"}`
	rr := serve(handler, http.MethodPost, "/to/"+addr+"/v1/x", strings.NewReader(body),
		withProxyToken(testAdminToken), withHeader("Expect", "100-continue"))
	got := <-received
	receipt, ok := storedReceipt(t, handler, rr)
	if !ok || receipt.ResponseStatus != http.StatusUnauthorized {
		t.Fatalf("receipt = %+v, %t", receipt, ok)
	}
	if receipt.RequestBodySHA256 != avr1.SHA256Hex(got) {
		t.Fatalf("Upstream received %q, Receipt names request_body_sha256 of %q", got, body)
	}
}
