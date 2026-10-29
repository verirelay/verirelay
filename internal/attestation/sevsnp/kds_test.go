package sevsnp

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-sev-guest/abi"
	"github.com/google/go-sev-guest/kds"
	sevtesting "github.com/google/go-sev-guest/testing"
)

func TestKDSGetterCachesVCEKByURL(t *testing.T) {
	chipID := make([]byte, abi.ChipIDSize)
	chipID[0] = 1
	url := kds.VCEKCertURL("Milan", chipID, 0x1234)
	var calls int
	vcek := testKDSVCEK(t)
	getter := NewKDSGetter(KDSGetterOptions{Client: testKDSClient(func(req *http.Request) *http.Response {
		calls++
		return responseBytes(http.StatusOK, vcek)
	})})

	first, err := getter.GetContext(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	first[0] = 'X'
	second, err := getter.GetContext(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !bytes.Equal(second, vcek) {
		t.Fatalf("calls = %d, second = %x", calls, second)
	}
}

func TestKDSGetterSeparatesVCEKChipAndTCBKeys(t *testing.T) {
	chipOne := make([]byte, abi.ChipIDSize)
	chipTwo := make([]byte, abi.ChipIDSize)
	chipOne[0] = 1
	chipTwo[0] = 2
	var calls int
	vcek := testKDSVCEK(t)
	getter := NewKDSGetter(KDSGetterOptions{Client: testKDSClient(func(req *http.Request) *http.Response {
		calls++
		return responseBytes(http.StatusOK, vcek)
	})})
	urls := []string{
		kds.VCEKCertURL("Milan", chipOne, 1),
		kds.VCEKCertURL("Milan", chipTwo, 1),
		kds.VCEKCertURL("Milan", chipOne, 2),
	}
	for _, url := range urls {
		if _, err := getter.GetContext(context.Background(), url); err != nil {
			t.Fatal(err)
		}
	}
	if calls != len(urls) {
		t.Fatalf("calls = %d, want %d", calls, len(urls))
	}
}

func TestKDSGetterCachesProductChainAndCoalescesMisses(t *testing.T) {
	url := kds.ProductCertChainURL(abi.VcekReportSigner, "Milan")
	chain := testKDSChain(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	getter := NewKDSGetter(KDSGetterOptions{Client: testKDSClient(func(req *http.Request) *http.Response {
		mu.Lock()
		calls++
		mu.Unlock()
		close(entered)
		<-release
		return responseBytes(http.StatusOK, chain)
	})})
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := getter.GetContext(context.Background(), url)
			errs <- err
		}()
	}
	<-entered
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestKDSGetterRetries429UsingRetryAfter(t *testing.T) {
	chipID := make([]byte, abi.ChipIDSize)
	url := kds.VCEKCertURL("Milan", chipID, 0)
	calls := 0
	var waits []time.Duration
	getter := NewKDSGetter(KDSGetterOptions{
		Client: testKDSClient(func(req *http.Request) *http.Response {
			calls++
			if calls < 3 {
				resp := response(http.StatusTooManyRequests, "limited")
				resp.Header.Set("Retry-After", "1")
				return resp
			}
			return responseBytes(http.StatusOK, testKDSVCEK(t))
		}),
		Sleep: func(ctx context.Context, delay time.Duration) error {
			waits = append(waits, delay)
			return nil
		},
	})
	if _, err := getter.GetContext(context.Background(), url); err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(waits) != 2 || waits[0] < time.Second || waits[1] < time.Second {
		t.Fatalf("calls = %d, waits = %v", calls, waits)
	}
}

func TestKDSGetterStopsRetryingWhenContextIsCanceled(t *testing.T) {
	chipID := make([]byte, abi.ChipIDSize)
	url := kds.VCEKCertURL("Milan", chipID, 0)
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	getter := NewKDSGetter(KDSGetterOptions{
		Client: testKDSClient(func(req *http.Request) *http.Response {
			calls++
			return response(http.StatusTooManyRequests, "limited")
		}),
		Sleep: func(ctx context.Context, delay time.Duration) error {
			cancel()
			<-ctx.Done()
			return ctx.Err()
		},
	})
	if _, err := getter.GetContext(ctx, url); err == nil {
		t.Fatal("canceled request was accepted")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestKDSGetterDoesNotCacheFailures(t *testing.T) {
	chipID := make([]byte, abi.ChipIDSize)
	url := kds.VCEKCertURL("Milan", chipID, 0)
	calls := 0
	getter := NewKDSGetter(KDSGetterOptions{
		Client: testKDSClient(func(req *http.Request) *http.Response {
			calls++
			return response(http.StatusServiceUnavailable, "down")
		}),
		MaxAttempts: 1,
	})
	for range 2 {
		if _, err := getter.GetContext(context.Background(), url); err == nil {
			t.Fatal("KDS failure was accepted")
		}
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestKDSGetterErrorOmitsURLQuery(t *testing.T) {
	url := kds.VCEKCertURL("Milan", make([]byte, abi.ChipIDSize), 0x1234)
	getter := NewKDSGetter(KDSGetterOptions{
		Client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("connection refused")
		})},
		MaxAttempts: 1,
	})
	_, err := getter.GetContext(context.Background(), url)
	if err == nil || strings.Contains(err.Error(), "?") || strings.Contains(err.Error(), "blSPL") {
		t.Fatalf("KDS error = %v", err)
	}
}

func TestKDSGetterRejectsUnrecognizedURL(t *testing.T) {
	getter := NewKDSGetter(KDSGetterOptions{})
	if _, err := getter.GetContext(context.Background(), "https://example.com/vcek"); err == nil {
		t.Fatal("unrecognized KDS URL was accepted")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func testKDSClient(handler func(*http.Request) *http.Response) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return handler(req), nil
	})}
}

func response(status int, body string) *http.Response {
	return responseBytes(status, []byte(body))
}

func responseBytes(status int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func testKDSVCEK(t *testing.T) []byte {
	t.Helper()
	signer, err := sevtesting.DefaultTestOnlyCertChain("Milan-B1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return signer.Vcek.Raw
}

func testKDSChain(t *testing.T) []byte {
	t.Helper()
	signer, err := sevtesting.DefaultTestOnlyCertChain("Milan-B1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return append(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: signer.Ask.Raw}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: signer.Ark.Raw})...,
	)
}
