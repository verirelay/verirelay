package server

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
)

// Delegate is the handler a Gate serves once it is installed.
type Delegate interface {
	http.Handler
	Ready(ctx context.Context) error
}

// Gate answers /healthz, and answers every other route with 503 not_ready
// until a Delegate is installed.
type Gate struct {
	mu       sync.RWMutex
	delegate Delegate
}

func NewGate() *Gate { return &Gate{} }

func (gate *Gate) Install(delegate Delegate) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.delegate = delegate
}

func (gate *Gate) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/healthz" {
		writeText(writer, http.StatusOK, "ok\n")
		return
	}
	delegate := gate.installed()
	if request.URL.Path == "/readyz" {
		if delegate == nil {
			writeText(writer, http.StatusServiceUnavailable, "not_ready\n")
			return
		}
		if err := delegate.Ready(request.Context()); err != nil {
			slog.Error("readiness check failed", "component", "server", "err", err)
			writeText(writer, http.StatusServiceUnavailable, "not_ready\n")
			return
		}
		writeText(writer, http.StatusOK, "ok\n")
		return
	}
	if delegate == nil {
		writer.Header().Set("Content-Type", "application/json")
		writeText(writer, http.StatusServiceUnavailable, "{\"error\":{\"code\":\"not_ready\",\"message\":\"service is not ready\"}}\n")
		return
	}
	delegate.ServeHTTP(writer, request)
}

func (gate *Gate) installed() Delegate {
	gate.mu.RLock()
	defer gate.mu.RUnlock()
	return gate.delegate
}

func writeText(writer http.ResponseWriter, status int, body string) {
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(body))
}
