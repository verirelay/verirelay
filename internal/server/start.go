package server

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/verirelay/verirelay/internal/config"
	"github.com/verirelay/verirelay/internal/provisioning"
)

const (
	initialRetryDelay = time.Second
	maxRetryDelay     = time.Minute
)

// Start provisions the Runtime Config, builds the server and installs it in
// gate. It retries every failure, logging each one, until it succeeds or ctx
// ends; it never falls back to another mode.
func Start(ctx context.Context, cfg config.Config, platform provisioning.Guest, gate *Gate) {
	retry(ctx, gate, func(ctx context.Context) (Delegate, error) {
		effective, err := provisioning.Resolve(ctx, cfg, platform)
		if err != nil {
			return nil, fmt.Errorf("provisioning: %w", err)
		}
		handler, err := New(ctx, effective)
		if err != nil {
			return nil, fmt.Errorf("initialize server: %w", err)
		}
		return handler, nil
	}, sleepContext)
}

func retry(ctx context.Context, gate *Gate, attempt func(context.Context) (Delegate, error), sleep func(context.Context, time.Duration) error) {
	for n := 1; ; n++ {
		delegate, err := attempt(ctx)
		if err == nil {
			gate.Install(delegate)
			slog.Info("server installed; readiness gate opened", "component", "server", "attempt", n)
			return
		}
		delay := retryDelay(n)
		slog.Error("startup failed; retrying", "component", "server", "attempt", n, "retry_in", delay.String(), "err", err)
		if err := sleep(ctx, delay); err != nil {
			slog.Error("startup stopped", "component", "server", "err", err)
			return
		}
	}
}

// retryDelay returns the wait after failed attempt n, counting from 1: 1 s,
// doubling up to 60 s, less up to 20% random jitter.
func retryDelay(attempt int) time.Duration {
	delay := maxRetryDelay
	if shift := attempt - 1; shift < 6 {
		delay = min(initialRetryDelay<<shift, maxRetryDelay)
	}
	return delay - rand.N(delay/5) //nolint:gosec // G404: jitter needs no cryptographic randomness
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
