package proxy

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/verirelay/protocol/avr1"
	"github.com/verirelay/verirelay/internal/storage"
)

func (h *Handler) handleAVRReceipt(w http.ResponseWriter, r *http.Request) {
	receipt, ok := h.lookupReceipt(w, r, avrReceiptPathPrefix)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(receipt)
}

func (h *Handler) lookupReceipt(w http.ResponseWriter, r *http.Request, pathPrefix string) (avr1.SignedReceiptV1, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		h.writeMethodNotAllowed(w, http.MethodGet)
		return avr1.SignedReceiptV1{}, false
	}
	id := strings.TrimPrefix(r.URL.Path, pathPrefix)
	if id == "" || strings.Contains(id, "/") {
		h.writeError(w, errorMissingRequestID, "")
		return avr1.SignedReceiptV1{}, false
	}
	if h.options.ReceiptStore == nil {
		h.writeError(w, errorReceiptStoreUnavailable, "")
		return avr1.SignedReceiptV1{}, false
	}
	receipt, ok, err := h.options.ReceiptStore.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrCorruptRecord) {
			h.writeError(w, errorReceiptStoreCorrupt, "")
			return avr1.SignedReceiptV1{}, false
		}
		h.writeError(w, errorReceiptStoreUnavailable, "")
		return avr1.SignedReceiptV1{}, false
	}
	if !ok {
		h.writeError(w, errorReceiptNotFound, "")
		return avr1.SignedReceiptV1{}, false
	}
	if receipt.RequestID != id || receipt.Signature == nil || avr1.VerifySignedReceiptV1(receipt) != nil {
		h.writeError(w, errorReceiptStoreCorrupt, "")
		return avr1.SignedReceiptV1{}, false
	}
	return receipt, true
}

func (h *Handler) handleAVREvidence(w http.ResponseWriter, r *http.Request) {
	receipt, ok := h.lookupReceipt(w, r, avrEvidencePathPrefix)
	if !ok {
		return
	}
	bundle := avr1.ReceiptEvidenceBundle{Version: avr1.AVRVersion, Receipt: receipt}
	if h.options.AttestationStore != nil {
		archived, found, err := h.options.AttestationStore.Get(r.Context(), receipt.KeyID)
		if err != nil {
			if errors.Is(err, storage.ErrCorruptAttestationRecord) {
				h.writeError(w, errorAttestationStoreCorrupt, "")
				return
			}
			h.writeError(w, errorAttestationStoreUnavailable, "")
			return
		}
		if found {
			bundle.Attestation = &archived
		}
	}
	h.writeJSON(w, http.StatusOK, bundle)
}
