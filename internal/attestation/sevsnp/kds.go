package sevsnp

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/go-sev-guest/abi"
	"github.com/google/go-sev-guest/kds"

	"github.com/verirelay/verirelay/internal/redact"
)

const (
	defaultKDSMaxAttempts     = 4
	defaultKDSInitialBackoff  = 250 * time.Millisecond
	defaultKDSMaxBackoff      = 2 * time.Second
	defaultKDSMaxRetryAfter   = 10 * time.Second
	defaultKDSAttemptTimeout  = 10 * time.Second
	defaultKDSMaxResponseSize = 1 << 20
)

type KDSGetterOptions struct {
	Client          *http.Client
	MaxAttempts     int
	InitialBackoff  time.Duration
	MaxBackoff      time.Duration
	AttemptTimeout  time.Duration
	MaxResponseSize int64
	Sleep           func(context.Context, time.Duration) error
}

type KDSGetter struct {
	client          *http.Client
	maxAttempts     int
	initialBackoff  time.Duration
	maxBackoff      time.Duration
	attemptTimeout  time.Duration
	maxResponseSize int64
	sleep           func(context.Context, time.Duration) error

	mu       sync.Mutex
	cache    map[string][]byte
	inflight map[string]*kdsCall
}

type kdsCall struct {
	done chan struct{}
	body []byte
	err  error
}

type kdsHTTPError struct {
	status     int
	retryAfter time.Duration
}

func (e *kdsHTTPError) Error() string {
	return fmt.Sprintf("AMD KDS returned HTTP %d", e.status)
}

func NewKDSGetter(opts KDSGetterOptions) *KDSGetter {
	if opts.Client == nil {
		opts.Client = &http.Client{}
	}
	client := *opts.Client
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = defaultKDSMaxAttempts
	}
	if opts.InitialBackoff <= 0 {
		opts.InitialBackoff = defaultKDSInitialBackoff
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = defaultKDSMaxBackoff
	}
	if opts.AttemptTimeout <= 0 {
		opts.AttemptTimeout = defaultKDSAttemptTimeout
	}
	if opts.MaxResponseSize <= 0 {
		opts.MaxResponseSize = defaultKDSMaxResponseSize
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepContext
	}
	return &KDSGetter{
		client:          &client,
		maxAttempts:     opts.MaxAttempts,
		initialBackoff:  opts.InitialBackoff,
		maxBackoff:      opts.MaxBackoff,
		attemptTimeout:  opts.AttemptTimeout,
		maxResponseSize: opts.MaxResponseSize,
		sleep:           opts.Sleep,
		cache:           make(map[string][]byte),
		inflight:        make(map[string]*kdsCall),
	}
}

func (g *KDSGetter) Get(url string) ([]byte, error) {
	return g.GetContext(context.Background(), url)
}

func (g *KDSGetter) GetContext(ctx context.Context, url string) ([]byte, error) {
	if g == nil {
		return nil, errors.New("AMD KDS getter is nil")
	}
	key, err := canonicalKDSURL(url)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	if body, ok := g.cache[key]; ok {
		g.mu.Unlock()
		return cloneBytes(body), nil
	}
	if call, ok := g.inflight[key]; ok {
		g.mu.Unlock()
		select {
		case <-call.done:
			return cloneBytes(call.body), call.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	call := &kdsCall{done: make(chan struct{})}
	g.inflight[key] = call
	g.mu.Unlock()

	body, err := g.fetch(ctx, key)
	g.mu.Lock()
	if err == nil {
		g.cache[key] = cloneBytes(body)
		call.body = cloneBytes(body)
	}
	call.err = err
	delete(g.inflight, key)
	close(call.done)
	g.mu.Unlock()
	return cloneBytes(body), err
}

func (g *KDSGetter) fetch(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < g.maxAttempts; attempt++ {
		body, err := g.fetchOnce(ctx, url)
		if err == nil {
			if err := validateKDSBody(url, body); err != nil {
				return nil, err
			}
			return body, nil
		}
		lastErr = err
		if !retryableKDSError(err) || attempt+1 == g.maxAttempts {
			break
		}
		delay := g.backoff(attempt)
		var httpErr *kdsHTTPError
		if errors.As(err, &httpErr) && httpErr.retryAfter > delay {
			delay = httpErr.retryAfter
		}
		if delay > defaultKDSMaxRetryAfter {
			delay = defaultKDSMaxRetryAfter
		}
		if err := g.sleep(ctx, delay); err != nil {
			return nil, fmt.Errorf("wait to retry AMD KDS request: %w", err)
		}
	}
	return nil, fmt.Errorf("fetch AMD KDS resource: %w", lastErr)
}

func (g *KDSGetter) fetchOnce(ctx context.Context, url string) ([]byte, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, g.attemptTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(attemptCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, redact.URLQuery(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &kdsHTTPError{status: resp.StatusCode, retryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, g.maxResponseSize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > g.maxResponseSize {
		return nil, fmt.Errorf("AMD KDS response exceeds %d bytes", g.maxResponseSize)
	}
	return body, nil
}

func (g *KDSGetter) backoff(attempt int) time.Duration {
	delay := g.initialBackoff
	for range attempt {
		if delay >= g.maxBackoff/2 {
			return g.maxBackoff
		}
		delay *= 2
	}
	if delay > g.maxBackoff {
		return g.maxBackoff
	}
	return delay
}

func canonicalKDSURL(rawURL string) (string, error) {
	if cert, err := kds.ParseVCEKCertURL(rawURL); err == nil {
		return kds.VCEKCertURL(cert.ProductLine, cert.HWID, kds.TCBVersion(cert.TCB)), nil
	}
	productLine, function, err := kds.ParseProductCertChainURL(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid AMD KDS URL: %w", err)
	}
	if function != kds.VcekCertFunction {
		return "", fmt.Errorf("unsupported AMD KDS certificate function %v", function)
	}
	return kds.ProductCertChainURL(abi.VcekReportSigner, productLine), nil
}

func validateKDSBody(url string, body []byte) error {
	if _, err := kds.ParseVCEKCertURL(url); err == nil {
		if _, err := x509.ParseCertificate(body); err != nil {
			return fmt.Errorf("parse AMD KDS VCEK: %w", err)
		}
		return nil
	}
	ask, ark, err := kds.ParseProductCertChain(body)
	if err != nil {
		return fmt.Errorf("parse AMD KDS product chain: %w", err)
	}
	if _, err := x509.ParseCertificate(ask); err != nil {
		return fmt.Errorf("parse AMD KDS ASK: %w", err)
	}
	if _, err := x509.ParseCertificate(ark); err != nil {
		return fmt.Errorf("parse AMD KDS ARK: %w", err)
	}
	return nil
}

func retryableKDSError(err error) bool {
	var httpErr *kdsHTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.status {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		default:
			return false
		}
	}
	return true
}

func retryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		return 0
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func cloneBytes(value []byte) []byte {
	return append([]byte(nil), value...)
}

var _ interface {
	Get(string) ([]byte, error)
	GetContext(context.Context, string) ([]byte, error)
} = (*KDSGetter)(nil)
