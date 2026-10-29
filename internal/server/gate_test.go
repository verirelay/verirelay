package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGateRejectsUntilInstalled(t *testing.T) {
	gate := NewGate()
	for _, path := range []string{"/healthz", "/readyz", "/to/api.example/v1"} {
		response := httptest.NewRecorder()
		gate.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		want := http.StatusServiceUnavailable
		if path == "/healthz" {
			want = http.StatusOK
		}
		if response.Code != want {
			t.Fatalf("%s status = %d, want %d", path, response.Code, want)
		}
		switch path {
		case "/healthz":
			if response.Body.String() != "ok\n" {
				t.Fatalf("%s body = %q, want plain-text health response", path, response.Body.String())
			}
		case "/readyz":
			if response.Body.String() != "not_ready\n" {
				t.Fatalf("%s body = %q, want plain-text readiness response", path, response.Body.String())
			}
		default:
			if response.Header().Get("Content-Type") != "application/json" || response.Body.String() != "{\"error\":{\"code\":\"not_ready\",\"message\":\"service is not ready\"}}\n" {
				t.Fatalf("%s response = %d %q content-type=%q", path, response.Code, response.Body.String(), response.Header().Get("Content-Type"))
			}
		}
	}
}

type fakeDelegate struct {
	readyErr error
	served   int
}

func (d *fakeDelegate) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	d.served++
	w.WriteHeader(http.StatusNoContent)
}

func (d *fakeDelegate) Ready(context.Context) error { return d.readyErr }

func TestGateDelegatesAfterInstall(t *testing.T) {
	for _, tc := range []struct {
		name       string
		readyErr   error
		readyzCode int
		readyzBody string
	}{
		{name: "ready", readyzCode: http.StatusOK, readyzBody: "ok\n"},
		{name: "delegate not ready", readyErr: errors.New("store down"), readyzCode: http.StatusServiceUnavailable, readyzBody: "not_ready\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate := NewGate()
			delegate := &fakeDelegate{readyErr: tc.readyErr}
			gate.Install(delegate)

			healthz := httptest.NewRecorder()
			gate.ServeHTTP(healthz, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))
			if healthz.Code != http.StatusOK || healthz.Body.String() != "ok\n" {
				t.Fatalf("/healthz = %d %q", healthz.Code, healthz.Body.String())
			}

			readyz := httptest.NewRecorder()
			gate.ServeHTTP(readyz, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/readyz", nil))
			if readyz.Code != tc.readyzCode || readyz.Body.String() != tc.readyzBody {
				t.Fatalf("/readyz = %d %q, want %d %q", readyz.Code, readyz.Body.String(), tc.readyzCode, tc.readyzBody)
			}

			proxied := httptest.NewRecorder()
			gate.ServeHTTP(proxied, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/to/api.example/v1", nil))
			if proxied.Code != http.StatusNoContent || delegate.served == 0 {
				t.Fatalf("/to delegated = %d served=%d", proxied.Code, delegate.served)
			}
		})
	}
}
