package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRetryKeepsTheGateClosedUntilAnAttemptSucceeds(t *testing.T) {
	gate := NewGate()
	var slept []time.Duration
	attempts := 0
	attempt := func(context.Context) (Delegate, error) {
		attempts++
		if attempts < 4 {
			if rr := serveGate(t, gate, "/readyz"); rr.Code != http.StatusServiceUnavailable {
				t.Fatalf("readyz before install = %d", rr.Code)
			}
			return nil, errors.New("veriprov unavailable")
		}
		return &fakeDelegate{}, nil
	}
	retry(context.Background(), gate, attempt, func(_ context.Context, delay time.Duration) error {
		slept = append(slept, delay)
		return nil
	})
	if attempts != 4 || len(slept) != 3 {
		t.Fatalf("attempts = %d, sleeps = %v", attempts, slept)
	}
	if rr := serveGate(t, gate, "/readyz"); rr.Code != http.StatusOK {
		t.Fatalf("readyz after install = %d", rr.Code)
	}
}

func TestRetryStopsWhenTheContextEnds(t *testing.T) {
	gate := NewGate()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts := 0
	retry(ctx, gate, func(context.Context) (Delegate, error) {
		attempts++
		return nil, errors.New("down")
	}, sleepContext)
	if attempts != 1 || gate.installed() != nil {
		t.Fatalf("attempts = %d, installed = %v", attempts, gate.installed())
	}
}

func TestRetryDelayDoublesFromOneSecondUpToAMinuteWithJitter(t *testing.T) {
	for attempt, nominal := range map[int]time.Duration{
		1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 6: 32 * time.Second,
		7: time.Minute, 8: time.Minute, 1000: time.Minute,
	} {
		for range 100 {
			delay := retryDelay(attempt)
			if delay > nominal || delay < nominal*4/5 {
				t.Fatalf("retryDelay(%d) = %v, want within 20%% below %v", attempt, delay, nominal)
			}
		}
	}
}

func serveGate(t *testing.T, gate *Gate, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	gate.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return rr
}
