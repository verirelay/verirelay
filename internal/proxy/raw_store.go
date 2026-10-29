package proxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/verirelay/protocol/avr1"
	"github.com/verirelay/protocol/veriprov1"
	"github.com/verirelay/verirelay/internal/storage"
)

// rawStoreIDBytes is the length of the random store_id before encoding.
const rawStoreIDBytes = 16

// rawStorePlan is the Raw Capture a client requested for one exchange.
type rawStorePlan struct {
	enabled bool
	ttl     time.Duration
	storeID string
	key     []byte
}

func (h *Handler) newRawStorePlan(headerValue string, requestID string) rawStorePlan {
	if h.options.RawStore == nil || strings.TrimSpace(headerValue) == "" {
		return rawStorePlan{}
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(headerValue), 10, 64)
	if err != nil || seconds <= 0 {
		slog.Warn("raw store header ignored", "component", "proxy", "request_id", requestID, "reason", "invalid_ttl")
		return rawStorePlan{}
	}
	ttl := veriprov1.DurationFromSeconds(seconds)
	if limit := h.options.RawStoreMaxTTL; limit > 0 && ttl > limit {
		ttl = limit
	}
	storeID := make([]byte, rawStoreIDBytes)
	key := make([]byte, avr1.RawCaptureKeySize)
	_, _ = rand.Read(storeID)
	_, _ = rand.Read(key)
	return rawStorePlan{enabled: true, ttl: ttl, storeID: base64.RawURLEncoding.EncodeToString(storeID), key: key}
}

func (h *Handler) setRawStoreHeaders(header http.Header, plan rawStorePlan) {
	if !plan.enabled {
		return
	}
	header.Set(storeIDHeader, plan.storeID)
	header.Set(storeKeyHeader, base64.RawURLEncoding.EncodeToString(plan.key))
}

func (h *Handler) saveRawExchangeFailOpen(plan rawStorePlan, requestBody []byte, responseBody []byte, requestID string) bool {
	if !plan.enabled || h.options.RawStore == nil {
		return false
	}
	data, err := encryptRawExchange(plan, requestBody, responseBody)
	if err != nil {
		slog.Error("raw store encryption failed", "component", "proxy", "request_id", requestID, "store_id", plan.storeID, "err", err)
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), rawStoreSaveTimeout)
	defer cancel()
	if err := h.options.RawStore.Save(ctx, plan.storeID, data, plan.ttl); err != nil {
		slog.Error("raw store persistence failed", "component", "proxy", "request_id", requestID, "store_id", plan.storeID, "err", err)
		return false
	}
	return true
}

func (h *Handler) deleteRawExchangeBestEffort(plan rawStorePlan, requestID string) {
	deleter, ok := h.options.RawStore.(storage.RawStoreDeleter)
	if !ok || !plan.enabled {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), rawStoreSaveTimeout)
	defer cancel()
	if err := deleter.Delete(ctx, plan.storeID); err != nil {
		slog.Error("raw store cleanup failed", "component", "proxy", "request_id", requestID, "store_id", plan.storeID, "err", err)
	}
}

// encryptRawExchange returns the Raw Capture envelope JSON of one exchange.
func encryptRawExchange(plan rawStorePlan, requestBody []byte, responseBody []byte) ([]byte, error) {
	capture, err := avr1.SealRawCapture(plan.key, plan.storeID, requestBody, responseBody)
	if err != nil {
		return nil, err
	}
	return json.Marshal(capture)
}

type rawStoreCapture struct {
	enabled bool
	limit   int64
	buf     bytes.Buffer
	over    bool
}

func newRawStoreCapture(enabled bool, limit int64) *rawStoreCapture {
	return &rawStoreCapture{enabled: enabled, limit: limit}
}

func (c *rawStoreCapture) write(data []byte) {
	if !c.enabled || c.over {
		return
	}
	if int64(c.buf.Len()+len(data)) > c.limit {
		c.over = true
		c.buf.Reset()
		return
	}
	_, _ = c.buf.Write(data)
}

func (c *rawStoreCapture) ok() bool { return c != nil && c.enabled && !c.over }
func (c *rawStoreCapture) bytes() []byte {
	if c == nil {
		return nil
	}
	return c.buf.Bytes()
}

func (h *Handler) handleAVRStore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	storeID := strings.TrimPrefix(r.URL.Path, avrStorePathPrefix)
	if storeID == "" || strings.Contains(storeID, "/") {
		h.writeError(w, errorMissingStoreID, "")
		return
	}
	if h.options.RawStore == nil {
		h.writeError(w, errorStoreNotFound, "")
		return
	}
	data, ok, err := h.options.RawStore.Get(r.Context(), storeID)
	if err != nil {
		if errors.Is(err, storage.ErrRawStoreCorrupt) {
			h.writeError(w, errorStoreCorrupt, "")
			return
		}
		h.writeError(w, errorStoreUnavailable, "")
		return
	}
	if !ok {
		h.writeError(w, errorStoreNotFound, "")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data) //nolint:gosec // G705: body is a JSON envelope served as application/json, not HTML
}
