package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/verirelay/protocol/avr1"

	"github.com/verirelay/verirelay/internal/redact"
)

var (
	errBodyTooLarge       = errors.New("body exceeds the maximum size")
	errUpstreamNotAllowed = errors.New("upstream is not allowed")
	errUpstreamUnresolved = errors.New("upstream could not be resolved")

	httpSchemePrefixes  = []string{"http://", "http%3A%2F%2F", "http%3a%2f%2f"}
	httpsSchemePrefixes = []string{"https://", "https%3A%2F%2F", "https%3a%2f%2f"}
)

func cutAnyPrefix(s string, prefixes []string) (string, bool) {
	for _, prefix := range prefixes {
		if rest, ok := strings.CutPrefix(s, prefix); ok {
			return rest, true
		}
	}
	return s, false
}

func (h *Handler) routeFor(ctx context.Context, requestURL *url.URL) (proxyRoute, error) {
	raw := strings.TrimPrefix(requestURL.EscapedPath(), upstreamPathPrefix)
	if raw == "" {
		return proxyRoute{}, errors.New("missing upstream target")
	}
	if _, ok := cutAnyPrefix(raw, httpSchemePrefixes); ok {
		return proxyRoute{}, errors.New("upstream scheme must be https")
	}
	if rest, ok := cutAnyPrefix(raw, httpsSchemePrefixes); ok {
		raw = rest
	}
	hostPart, pathPart, _ := strings.Cut(raw, "/")
	hostValue, err := url.PathUnescape(hostPart)
	if err != nil {
		return proxyRoute{}, errors.New("invalid upstream host")
	}
	hostValue = strings.ToLower(strings.TrimSpace(hostValue))
	resolvedIPs, err := h.validateUpstreamHost(ctx, hostValue)
	if err != nil {
		return proxyRoute{}, err
	}
	if pathPart == "" {
		pathPart = "/"
	} else {
		pathPart = "/" + pathPart
	}
	// pathPart is the escaped path form taken from requestURL.EscapedPath().
	// url.URL.Path must hold the unescaped form, otherwise String()/EscapedPath()
	// would percent-encode the path a second time (e.g. %20 -> %2520). Keep the
	// escaped form in RawPath so the original encoding is forwarded verbatim.
	pathValue, err := url.PathUnescape(pathPart)
	if err != nil {
		return proxyRoute{}, errors.New("invalid upstream path")
	}
	target := url.URL{Scheme: "https", Host: hostValue, Path: pathValue, RawPath: pathPart, RawQuery: requestURL.RawQuery}
	upstream := url.URL{Scheme: "https", Host: hostValue, Path: "/"}
	return proxyRoute{upstream: &upstream, target: target, resolvedIPs: resolvedIPs}, nil
}

func (h *Handler) validateUpstreamHost(ctx context.Context, host string) ([]netip.Addr, error) {
	parsedHost, err := h.upstreamHostName(host)
	if err != nil {
		return nil, err
	}
	if h.options.AllowPrivateUpstreams {
		return nil, nil
	}
	if isLocalHostname(parsedHost) {
		return nil, fmt.Errorf("%w: private upstream host is not allowed", errUpstreamNotAllowed)
	}
	return resolvePublicUpstreamIPs(ctx, parsedHost)
}

func (h *Handler) upstreamHostName(host string) (string, error) {
	if host == "" {
		return "", errors.New("missing upstream host")
	}
	parsedHost := host
	if strings.Contains(host, "@") {
		return "", errors.New("upstream host must not include userinfo")
	}
	if strings.Contains(host, ":") {
		name, port, err := net.SplitHostPort(host)
		if err != nil {
			return "", errors.New("invalid upstream host")
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", errors.New("invalid upstream port")
		}
		parsedHost = name
	}
	return strings.TrimSuffix(strings.ToLower(parsedHost), "."), nil
}

func isLocalHostname(name string) bool {
	return name == "localhost" || strings.HasSuffix(name, ".localhost") || strings.HasSuffix(name, ".local")
}

func resolvePublicUpstreamIPs(ctx context.Context, name string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(strings.Trim(name, "[]")); err == nil {
		if !isPublicUpstreamIP(ip) {
			return nil, fmt.Errorf("%w: private upstream ip is not allowed", errUpstreamNotAllowed)
		}
		return []netip.Addr{ip}, nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, name)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("%w: upstream host could not be resolved", errUpstreamUnresolved)
	}
	resolved := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok || !isPublicUpstreamIP(addr) {
			return nil, fmt.Errorf("%w: upstream host resolves to a private ip", errUpstreamNotAllowed)
		}
		resolved = append(resolved, addr)
	}
	return resolved, nil
}

func isPublicUpstreamIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsMulticast() && !ip.IsUnspecified()
}

func newUpstreamClient(transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func (h *Handler) clientForRoute(route proxyRoute) *http.Client {
	if h.options.AllowPrivateUpstreams || len(route.resolvedIPs) == 0 || h.transport == nil {
		return h.client
	}
	transport := h.transport.Clone()
	baseDialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network string, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(strings.TrimSuffix(host, "."), route.upstream.Hostname()) {
			return nil, errors.New("unexpected upstream dial host")
		}
		var lastErr error
		for _, ip := range route.resolvedIPs {
			conn, err := baseDialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, errors.New("no resolved upstream ip")
	}
	return newUpstreamClient(transport)
}

func (h *Handler) handleProxy(w http.ResponseWriter, r *http.Request) {
	if !h.tokenMatches(r.Header.Get(proxyTokenHeader)) {
		h.writeError(w, errorUnauthorized, "")
		return
	}
	requestID := NewRequestID()
	if requestID == "" {
		h.writeError(w, errorRequestIDFailed, "")
		return
	}
	storePlan := h.newRawStorePlan(r.Header.Get(storeRequestHeader), requestID)

	route, failure := h.resolveRoute(r.Context(), r.URL)
	if failure != nil {
		h.writeProxyError(w, failure.code, requestID, failure.detail)
		return
	}
	client := h.clientForRoute(route)
	clientCtx := r.Context()

	bodyBytes, err := readLimited(r.Body, maxRequestBodyBytes)
	defer func() { _ = r.Body.Close() }()
	if err != nil {
		if errors.Is(err, errBodyTooLarge) {
			h.writeProxyError(w, errorRequestBodyTooLarge, requestID, err.Error())
		} else {
			h.writeProxyError(w, errorRequestBodyReadFailed, requestID, err.Error())
		}
		return
	}
	requestBodySHA256 := avr1.SHA256Hex(bodyBytes)

	outReq, failure := h.buildUpstreamRequest(r, route, bodyBytes, requestID)
	if failure != nil {
		h.writeProxyError(w, failure.code, requestID, failure.detail)
		return
	}

	resp, err := client.Do(outReq)
	if err != nil {
		err = redact.URLQuery(err)
		if clientCtx.Err() != nil {
			slog.Error("upstream request failed after client context ended", "component", "proxy", "request_id", requestID, "err", err)
			return
		}
		failure := upstreamRequestFailure(err)
		h.writeProxyError(w, failure.code, requestID, failure.detail)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	ex := &exchange{
		requestID:          requestID,
		method:             r.Method,
		route:              route,
		requestBody:        bodyBytes,
		requestBodySHA256:  requestBodySHA256,
		resp:               resp,
		upstreamSPKISHA256: upstreamSPKI(resp),
		storePlan:          storePlan,
		clientCtx:          clientCtx,
	}
	if isStreamingResponse(resp.Header) {
		h.handleStreamingResponse(w, r, ex)
		return
	}
	h.handleBufferedResponse(w, ex)
}

type proxyFailure struct {
	code   errorCode
	detail string
}

type exchange struct {
	requestID          string
	method             string
	route              proxyRoute
	requestBody        []byte
	requestBodySHA256  string
	resp               *http.Response
	upstreamSPKISHA256 string
	storePlan          rawStorePlan
	clientCtx          context.Context
}

func (h *Handler) resolveRoute(ctx context.Context, u *url.URL) (proxyRoute, *proxyFailure) {
	route, err := h.routeFor(ctx, u)
	if err != nil {
		code := errorUpstreamRouteInvalid
		if errors.Is(err, errUpstreamNotAllowed) {
			code = errorUpstreamNotAllowed
		} else if errors.Is(err, errUpstreamUnresolved) {
			code = errorUpstreamRequestFailed
		}
		return proxyRoute{}, &proxyFailure{code: code, detail: err.Error()}
	}
	return route, nil
}

func (h *Handler) buildUpstreamRequest(r *http.Request, route proxyRoute, body []byte, requestID string) (*http.Request, *proxyFailure) {
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, route.target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, &proxyFailure{code: errorRequestBuildFailed, detail: err.Error()}
	}
	copyHeaders(outReq.Header, r.Header)
	outReq.ContentLength = int64(len(body))
	outReq.Host = route.upstream.Host
	if _, ok := outReq.Header["User-Agent"]; !ok {
		// An empty value stops net/http from sending its default User-Agent.
		outReq.Header.Set("User-Agent", "")
	}
	outReq.Header.Set(requestIDHeader, requestID)
	return outReq, nil
}

func upstreamRequestFailure(err error) *proxyFailure {
	if isTimeoutError(err) {
		return &proxyFailure{code: errorUpstreamTimeout, detail: err.Error()}
	}
	return &proxyFailure{code: errorUpstreamRequestFailed, detail: err.Error()}
}

func isTimeoutError(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && networkError.Timeout()
}

func (h *Handler) handleBufferedResponse(w http.ResponseWriter, ex *exchange) {
	body, readErr := readLimited(ex.resp.Body, maxBufferedResponseBytes)
	if readErr != nil {
		if errors.Is(readErr, errBodyTooLarge) {
			h.writeProxyError(w, errorUpstreamResponseTooLarge, ex.requestID, readErr.Error())
			return
		}
		if ex.clientCtx.Err() != nil {
			slog.Error("upstream response read failed after client context ended", "component", "proxy", "request_id", ex.requestID, "err", readErr)
			return
		}
		code := errorResponseBodyReadFailed
		if isTimeoutError(readErr) {
			code = errorUpstreamTimeout
		}
		h.writeProxyError(w, code, ex.requestID, readErr.Error())
		return
	}

	storeSaved := h.saveRawExchangeFailOpen(ex.storePlan, ex.requestBody, body, ex.requestID)
	h.writeUpstreamHeader(w, ex, storeSaved)
	written, writeErr := w.Write(body)
	if writeErr != nil || written != len(body) {
		if storeSaved {
			h.deleteRawExchangeBestEffort(ex.storePlan, ex.requestID)
		}
		return
	}
	h.publishReceipt(ex, avr1.SHA256Hex(body))
}

func (h *Handler) writeUpstreamHeader(w http.ResponseWriter, ex *exchange, withStoreHeaders bool) {
	copyHeaders(w.Header(), ex.resp.Header)
	w.Header().Set(requestIDHeader, ex.requestID)
	if withStoreHeaders {
		h.setRawStoreHeaders(w.Header(), ex.storePlan)
	}
	w.WriteHeader(ex.resp.StatusCode)
}

func (h *Handler) handleStreamingResponse(w http.ResponseWriter, r *http.Request, ex *exchange) {
	h.writeUpstreamHeader(w, ex, ex.storePlan.enabled)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	capture := newRawStoreCapture(ex.storePlan.enabled, maxBufferedResponseBytes)
	responseBodySHA256, err := copyStreamingResponseBody(r.Context(), w, ex.resp.Body, capture)
	if err != nil {
		slog.Error("sse response copy failed", "component", "proxy", "request_id", ex.requestID, "err", err)
		return
	}
	if capture.ok() {
		h.saveRawExchangeFailOpen(ex.storePlan, ex.requestBody, capture.bytes(), ex.requestID)
	}
	h.publishReceipt(ex, responseBodySHA256)
}

type streamClientWriteError struct{ err error }

func (e *streamClientWriteError) Error() string { return e.err.Error() }

func (e *streamClientWriteError) Unwrap() error { return e.err }

func copyStreamingResponseBody(ctx context.Context, w http.ResponseWriter, src io.Reader, capture *rawStoreCapture) (string, error) {
	flusher, _ := w.(http.Flusher)
	copier := streamCopier{dst: w, flusher: flusher, hash: sha256.New(), capture: capture}
	buf := make([]byte, 32*1024)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			if err := copier.writeChunk(buf[:n]); err != nil {
				return "", err
			}
			if err := streamContextError(ctx, readErr); err != nil {
				return "", err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return copier.sum(), nil
			}
			return "", readErr
		}
	}
}

func streamContextError(ctx context.Context, readErr error) error {
	if errors.Is(readErr, io.EOF) {
		return nil
	}
	return ctx.Err()
}

type streamCopier struct {
	dst     io.Writer
	flusher http.Flusher
	hash    hash.Hash
	capture *rawStoreCapture
}

func (c *streamCopier) writeChunk(chunk []byte) error {
	n, writeErr := c.dst.Write(chunk)
	if c.flusher != nil {
		c.flusher.Flush()
	}
	if writeErr != nil {
		return &streamClientWriteError{err: writeErr}
	}
	if n != len(chunk) {
		return &streamClientWriteError{err: io.ErrShortWrite}
	}
	_, _ = c.hash.Write(chunk)
	if c.capture != nil {
		c.capture.write(chunk)
	}
	return nil
}

func (c *streamCopier) sum() string {
	return hex.EncodeToString(c.hash.Sum(nil))
}

func (h *Handler) publishReceipt(ex *exchange, responseBodySHA256 string) {
	signed, err := h.buildSignedReceipt(ex, responseBodySHA256)
	if err != nil {
		slog.Error("receipt signing failed", "component", "proxy", "request_id", ex.requestID, "err", err)
		return
	}
	if h.options.ReceiptStore == nil {
		slog.Error("receipt store unavailable", "component", "proxy", "request_id", ex.requestID)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.options.ReceiptStore.Save(ctx, ex.requestID, signed); err != nil {
		slog.Error("receipt persistence failed", "component", "proxy", "request_id", ex.requestID, "err", err)
		return
	}
	if h.options.AttestationStore == nil || h.options.ArchivedAttestation == nil || signed.KeyID != h.options.ArchivedAttestation.KeyID || h.options.ArchivedAttestationTTL <= 0 {
		return
	}
	if err := h.options.AttestationStore.Save(ctx, signed.KeyID, *h.options.ArchivedAttestation, h.options.ArchivedAttestationTTL); err != nil {
		slog.Error("archived attestation refresh failed", "component", "proxy", "request_id", ex.requestID, "key_id", signed.KeyID, "err", err)
	}
}

func (h *Handler) buildSignedReceipt(ex *exchange, responseBodySHA256 string) (avr1.SignedReceiptV1, error) {
	if h.options.ReceiptSigner == nil {
		return avr1.SignedReceiptV1{}, errors.New("receipt signer not configured")
	}
	return avr1.BuildSignedReceiptV1(avr1.ReceiptFactsV1{
		RequestID:          ex.requestID,
		Timestamp:          time.Now().UTC().Format(time.RFC3339Nano),
		Method:             ex.method,
		UpstreamAuthority:  ex.route.upstream.Host,
		UpstreamPath:       upstreamPathWithQuery(ex.route.target),
		SNI:                ex.route.upstream.Hostname(),
		UpstreamSPKISHA256: ex.upstreamSPKISHA256,
		RequestBodySHA256:  ex.requestBodySHA256,
		ResponseBodySHA256: responseBodySHA256,
		ResponseStatus:     ex.resp.StatusCode,
	}, h.options.ReceiptSigner)
}

// upstreamSPKI returns the hex SHA-256 of the Upstream certificate's SPKI, or
// "" without a TLS peer certificate, in which case no Receipt can be signed.
func upstreamSPKI(resp *http.Response) string {
	if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
		return ""
	}
	digest := sha256.Sum256(resp.TLS.PeerCertificates[0].RawSubjectPublicKeyInfo)
	return hex.EncodeToString(digest[:])
}

func upstreamPathWithQuery(u url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return path
}

func readLimited(src io.Reader, limit int64) ([]byte, error) {
	limited := io.LimitReader(src, limit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w: body exceeds %d bytes", errBodyTooLarge, limit)
	}
	return body, nil
}

func isStreamingResponse(h http.Header) bool {
	contentType := strings.ToLower(h.Get("Content-Type"))
	return strings.Contains(contentType, "text/event-stream")
}

func copyHeaders(dst, src http.Header) {
	connectionHeaders := make(map[string]struct{})
	for _, value := range src.Values("Connection") {
		for key := range strings.SplitSeq(value, ",") {
			if key = strings.TrimSpace(key); key != "" {
				connectionHeaders[strings.ToLower(key)] = struct{}{}
			}
		}
	}
	for key, values := range src {
		if _, ok := connectionHeaders[strings.ToLower(key)]; ok || shouldSkipHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func shouldSkipHeader(key string) bool {
	lower := strings.ToLower(key)
	if strings.HasPrefix(lower, "x-verirelay-") {
		return true
	}
	switch lower {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "proxy-connection", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}
