package proxy

import (
	"crypto/tls"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/verirelay/verirelay/internal/storage"
)

const (
	attestationPath          = "/v1/avr/attestation"
	avrReceiptPathPrefix     = "/v1/avr/receipts/"
	avrEvidencePathPrefix    = "/v1/avr/evidence/"
	avrStorePathPrefix       = "/v1/avr/store/"
	upstreamPathPrefix       = "/to/"
	proxyTokenHeader         = "X-VeriRelay-Token" // #nosec G101 -- HTTP header name, not a credential
	maxRequestBodyBytes      = 32 << 20
	maxBufferedResponseBytes = 64 << 20
	attestationMaxWaiting    = 8
	attestationRetryAfter    = "1"
	requestIDHeader          = "X-VeriRelay-Request-Id"
	storeRequestHeader       = "X-VeriRelay-Store"
	storeIDHeader            = "X-VeriRelay-Store-Id"
	storeKeyHeader           = "X-VeriRelay-Store-Key"
	rawStoreSaveTimeout      = 500 * time.Millisecond
)

type proxyRoute struct {
	upstream    *url.URL
	target      url.URL
	resolvedIPs []netip.Addr
}

type Handler struct {
	client           *http.Client
	transport        *http.Transport
	debugErrors      bool
	options          Options
	attestationAdmit chan struct{}
	attestationRun   chan struct{}
}

func NewHandler(debugErrors bool, options Options) *Handler {
	// net/http initializes DefaultTransport as *http.Transport and this program never replaces it.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DisableKeepAlives = true
	transport.ForceAttemptHTTP2 = false
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	transport.DisableCompression = true
	h := &Handler{
		client:           newUpstreamClient(transport),
		transport:        transport,
		debugErrors:      debugErrors,
		options:          options,
		attestationAdmit: make(chan struct{}, attestationMaxWaiting+1),
		attestationRun:   make(chan struct{}, 1),
	}
	if h.options.ReceiptStore == nil {
		h.options.ReceiptStore = storage.NewMemoryReceiptStore()
	}
	if h.options.AttestationStore == nil {
		h.options.AttestationStore = storage.NewMemoryAttestationStore()
	}
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	case r.URL.Path == "/readyz":
		h.handleReady(w, r)
	case r.URL.Path == attestationPath:
		h.handleAttestation(w, r)
	case strings.HasPrefix(r.URL.Path, avrReceiptPathPrefix):
		h.handleAVRReceipt(w, r)
	case strings.HasPrefix(r.URL.Path, avrEvidencePathPrefix):
		h.handleAVREvidence(w, r)
	case strings.HasPrefix(r.URL.Path, avrStorePathPrefix):
		h.handleAVRStore(w, r)
	case strings.HasPrefix(r.URL.Path, upstreamPathPrefix):
		h.handleProxy(w, r)
	default:
		h.writeError(w, errorNotFound, "")
	}
}
