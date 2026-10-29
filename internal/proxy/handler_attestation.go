package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/verirelay/protocol/avr1"
	"github.com/verirelay/verirelay/internal/attestation"
)

func (h *Handler) handleAttestation(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w, http.MethodGet)
		return
	}
	nonceValue := r.URL.Query().Get("nonce")
	if nonceValue == "" {
		h.writeError(w, errorMissingNonce, "")
		return
	}
	nonce, err := avr1.DecodeNonceBase64URL(nonceValue)
	if err != nil {
		h.writeError(w, errorInvalidNonce, "")
		return
	}
	if h.options.AttestationManager == nil || h.options.ReceiptSigner == nil {
		h.writeError(w, errorAttestationUnavailable, "")
		return
	}
	publicKey, err := avr1.DecodeEd25519PublicKeyBase64URL(h.options.ReceiptSigner.PublicKeyBase64URL())
	if err != nil {
		h.writeError(w, errorReceiptPublicKeyInvalid, "")
		return
	}
	release, busy, ok := h.acquireAttestationSlot(r.Context())
	if busy {
		w.Header().Set("Retry-After", attestationRetryAfter)
		h.writeError(w, errorAttestationBusy, "")
		return
	}
	if !ok {
		return
	}
	defer release()

	report, err := h.options.AttestationManager.Attestation(r.Context(), attestation.Request{
		Nonce:            nonce,
		ReceiptPublicKey: publicKey,
		KeyID:            h.options.ReceiptSigner.KeyID(),
	})
	if err != nil {
		code := errorAttestationReportFailed
		switch {
		case errors.Is(err, attestation.ErrQuoteProviderUnavailable):
			code = errorAttestationUnavailable
		case errors.Is(err, attestation.ErrDisclosureUnavailable), errors.Is(err, attestation.ErrInvalidManifest):
			code = errorAttestationDisclosureFailed
		case errors.Is(err, attestation.ErrUnsupportedReportSigner):
			code = errorAttestationReportUnsupported
		}
		slog.Error("attestation failed", "component", "proxy", "err", err)
		h.writeError(w, code, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

// acquireAttestationSlot returns busy when the queue is full and not-ok when the context ends while waiting.
func (h *Handler) acquireAttestationSlot(ctx context.Context) (release func(), busy bool, ok bool) {
	select {
	case h.attestationAdmit <- struct{}{}:
	default:
		return nil, true, false
	}
	select {
	case h.attestationRun <- struct{}{}:
		return func() {
			<-h.attestationRun
			<-h.attestationAdmit
		}, false, true
	case <-ctx.Done():
		<-h.attestationAdmit
		return nil, false, false
	}
}
