package server

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"
)

// dialRaw starts a server for handler and opens a client connection to it
// that the test writes by hand. The connection closes before the server, so
// a server still reading it cannot block the server's Close.
func dialRaw(t *testing.T, handler http.Handler) (net.Conn, *bufio.Reader) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	conn, err := new(net.Dialer).DialContext(t.Context(), "tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return conn, bufio.NewReader(conn)
}

// readResponse reads a whole response and returns its status and whether it
// carries Connection: close.
func readResponse(t *testing.T, reader *bufio.Reader) (int, bool) {
	t.Helper()
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Close
}

func TestUnreadRequestBodyDoesNotHoldTheConnection(t *testing.T) {
	for name, responseBytes := range map[string]int{"short response": 3, "response flushed by the handler": 64 << 10} {
		t.Run(name, func(t *testing.T) {
			conn, reader := dialRaw(t, closeUnreadBody(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, strings.Repeat("a", responseBytes))
			})))
			// The client sends 10 of the 200000 body bytes it announces, and no more.
			if _, err := io.WriteString(conn, "GET /v1/avr/receipts/x HTTP/1.1\r\nHost: relay\r\nContent-Length: 200000\r\n\r\n0123456789"); err != nil {
				t.Fatal(err)
			}
			if status, closing := readResponse(t, reader); status != http.StatusOK || !closing {
				t.Fatalf("response = %d, Connection: close = %t", status, closing)
			}
			if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) && !errors.Is(err, syscall.ECONNRESET) {
				t.Fatalf("read after the response = %v, want the connection closed", err)
			}
		})
	}
}

func TestRequestBodyReadToTheEndKeepsTheConnection(t *testing.T) {
	conn, reader := dialRaw(t, closeUnreadBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, "ok")
	})))
	for _, request := range []string{
		"POST /to/example.com/v1 HTTP/1.1\r\nHost: relay\r\nContent-Length: 4\r\n\r\nbody",
		"POST /to/example.com/v1 HTTP/1.1\r\nHost: relay\r\nTransfer-Encoding: chunked\r\n\r\n4\r\nbody\r\n0\r\n\r\n",
		"GET /healthz HTTP/1.1\r\nHost: relay\r\n\r\n",
	} {
		if _, err := io.WriteString(conn, request); err != nil {
			t.Fatal(err)
		}
		if status, closing := readResponse(t, reader); status != http.StatusOK || closing {
			t.Fatalf("response to %q = %d, Connection: close = %t", request, status, closing)
		}
	}
}

func TestConnectionLimitStaysWellBelowTheOpenFileLimit(t *testing.T) {
	for openFiles, want := range map[uint64]int{1024: 256, 4096: 1024, 1 << 16: 4096, 1 << 20: 4096} {
		if got := connectionLimit(openFiles); got != want {
			t.Errorf("connectionLimit(%d) = %d, want %d", openFiles, got, want)
		}
	}
}
