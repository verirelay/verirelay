package proxy

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/verirelay/verirelay/internal/storage"
)

// Ready reports whether the stores the handler depends on pass their readiness checks.
func (h *Handler) Ready(ctx context.Context) error {
	for _, store := range []any{h.options.ReceiptStore, h.options.AttestationStore} {
		checker, ok := store.(storage.ReadinessChecker)
		if !ok {
			continue
		}
		if err := checkReady(ctx, checker); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) handleReady(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	if err := h.Ready(r.Context()); err != nil {
		slog.Error("readiness check failed", "component", "proxy", "err", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("not_ready\n"))
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func checkReady(ctx context.Context, checker storage.ReadinessChecker) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return checker.Ready(ctx)
}
