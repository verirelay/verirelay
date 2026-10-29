package proxy

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/verirelay/verirelay/internal/attestation"
)

const attestationQueueTestNonce = "abcdef0123456789abcdef0123456789"

type queueTestQuoteProvider struct {
	unblock chan struct{}
	started chan struct{}
	calls   atomic.Int64
	active  atomic.Int64
	max     atomic.Int64
}

func newQueueTestQuoteProvider() *queueTestQuoteProvider {
	return &queueTestQuoteProvider{
		unblock: make(chan struct{}),
		started: make(chan struct{}, attestationMaxWaiting+2),
	}
}

func (p *queueTestQuoteProvider) Quote(ctx context.Context, req attestation.QuoteRequest) (attestation.Quote, error) {
	active := p.active.Add(1)
	defer p.active.Add(-1)
	p.calls.Add(1)
	for maximum := p.max.Load(); active > maximum; maximum = p.max.Load() {
		if p.max.CompareAndSwap(maximum, active) {
			break
		}
	}
	p.started <- struct{}{}
	select {
	case <-p.unblock:
	case <-ctx.Done():
		return attestation.Quote{}, ctx.Err()
	}
	return (attestation.FakeProvider{}).Quote(ctx, req)
}

func TestAttestationRequestsAreSerialized(t *testing.T) {
	const requests = 6
	provider := newQueueTestQuoteProvider()
	handler := newAttestationQueueTestHandler(t, provider)
	responses := make(chan *httptest.ResponseRecorder, requests)
	for range requests {
		go func() {
			responses <- serveAttestationQueueTestRequest(handler, context.Background())
		}()
	}
	waitForQuoteStart(t, provider)
	waitForAttestationAdmissionCount(t, handler, requests)
	close(provider.unblock)
	for range requests {
		if response := <-responses; response.Code != http.StatusOK {
			t.Fatalf("attestation response = %d %s", response.Code, response.Body.String())
		}
	}
	if got := provider.max.Load(); got != 1 {
		t.Fatalf("maximum concurrent quote calls = %d, want 1", got)
	}
}

func TestAttestationQueueFullReturnsBusy(t *testing.T) {
	const admitted = attestationMaxWaiting + 1
	provider := newQueueTestQuoteProvider()
	handler := newAttestationQueueTestHandler(t, provider)
	responses := make(chan *httptest.ResponseRecorder, admitted)
	for range admitted {
		go func() {
			responses <- serveAttestationQueueTestRequest(handler, context.Background())
		}()
	}
	waitForQuoteStart(t, provider)
	waitForAttestationAdmissionCount(t, handler, admitted)

	callsBeforeBusy := provider.calls.Load()
	busy := serveAttestationQueueTestRequest(handler, context.Background())
	if busy.Code != http.StatusServiceUnavailable || decodeErrorResponse(t, busy).Code != errorAttestationBusy {
		t.Fatalf("busy response = %d %s", busy.Code, busy.Body.String())
	}
	if got := busy.Header().Get("Retry-After"); got != attestationRetryAfter {
		t.Fatalf("Retry-After = %q, want %q", got, attestationRetryAfter)
	}
	if got := provider.calls.Load(); got != callsBeforeBusy {
		t.Fatalf("provider calls after busy response = %d, want unchanged at %d", got, callsBeforeBusy)
	}

	close(provider.unblock)
	for range admitted {
		if response := <-responses; response.Code != http.StatusOK {
			t.Fatalf("admitted attestation response = %d %s", response.Code, response.Body.String())
		}
	}
}

func TestCancelledAttestationWaiterFreesAdmissionSlot(t *testing.T) {
	provider := newQueueTestQuoteProvider()
	handler := newAttestationQueueTestHandler(t, provider)
	runningResponse := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		runningResponse <- serveAttestationQueueTestRequest(handler, context.Background())
	}()
	waitForQuoteStart(t, provider)

	waitingContext, cancelWaiting := context.WithCancel(context.Background())
	waitingResponse := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		waitingResponse <- serveAttestationQueueTestRequest(handler, waitingContext)
	}()
	waitForAttestationAdmissionCount(t, handler, 2)
	cancelWaiting()
	waitForAttestationAdmissionCount(t, handler, 1)
	<-waitingResponse
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want 1 after waiter cancellation", got)
	}

	close(provider.unblock)
	if response := <-runningResponse; response.Code != http.StatusOK {
		t.Fatalf("running attestation response = %d %s", response.Code, response.Body.String())
	}
}

func newAttestationQueueTestHandler(t *testing.T, provider *queueTestQuoteProvider) *Handler {
	t.Helper()
	handler := testHandler(t)
	handler.options.AttestationManager = attestation.NewManagerWithOptions(provider, attestation.ManagerOptions{DisclosureReader: testDisclosureReader{}})
	return handler
}

func serveAttestationQueueTestRequest(handler *Handler, ctx context.Context) *httptest.ResponseRecorder {
	target := attestationPath + "?nonce=" + base64.RawURLEncoding.EncodeToString([]byte(attestationQueueTestNonce))
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func waitForQuoteStart(t *testing.T, provider *queueTestQuoteProvider) {
	t.Helper()
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for quote provider")
	}
}

func waitForAttestationAdmissionCount(t *testing.T, handler *Handler, want int) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for len(handler.attestationAdmit) != want {
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("attestation admission count = %d, want %d", len(handler.attestationAdmit), want)
		}
	}
}
