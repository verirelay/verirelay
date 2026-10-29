package proxy

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"net/http"
)

type errorResponseBody struct {
	Error errorResponseDetail `json:"error"`
}

type errorResponseDetail struct {
	Code      errorCode `json:"code"`
	Message   string    `json:"message"`
	RequestID string    `json:"request_id,omitempty"`
}

// tokenMatches reports whether token equals the configured admin token. An
// unconfigured admin token matches nothing.
func (h *Handler) tokenMatches(token string) bool {
	if h.options.AdminToken == "" || token == "" {
		return false
	}
	providedHash := sha256.Sum256([]byte(token))
	expectedHash := sha256.Sum256([]byte(h.options.AdminToken))
	return subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) == 1
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (h *Handler) writeError(w http.ResponseWriter, code errorCode, detail string) {
	h.writeErrorBody(w, code, "", detail)
}

func (h *Handler) writeProxyError(w http.ResponseWriter, code errorCode, requestID string, detail string) {
	w.Header().Set(requestIDHeader, requestID)
	h.writeErrorBody(w, code, requestID, detail)
}

func (h *Handler) writeErrorBody(w http.ResponseWriter, code errorCode, requestID string, detail string) {
	h.writeJSON(w, errorSpecs[code].status, errorResponseBody{Error: errorResponseDetail{
		Code:      code,
		Message:   h.errorMessage(code, detail),
		RequestID: requestID,
	}})
}

func (h *Handler) writeMethodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	h.writeError(w, errorMethodNotAllowed, "")
}

func (h *Handler) errorMessage(code errorCode, detail string) string {
	message := errorSpecs[code].message
	if h.debugErrors && detail != "" {
		message += ": " + detail
	}
	return message
}
