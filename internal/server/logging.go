package server

import (
	"log/slog"
	"net/http"
	"time"
)

const requestIDHeader = "X-VeriRelay-Request-Id"

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (writer *loggingResponseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *loggingResponseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	written, err := writer.ResponseWriter.Write(body)
	writer.bytes += written
	return written, err
}

func (writer *loggingResponseWriter) Flush() {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// LoggingMiddleware records request metadata without reading request or response bodies.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		response := &loggingResponseWriter{ResponseWriter: writer}
		next.ServeHTTP(response, request)
		if response.status == 0 {
			response.status = http.StatusOK
		}

		level := slog.LevelInfo
		if request.URL.Path == "/healthz" || request.URL.Path == "/readyz" {
			level = slog.LevelDebug
		}
		attrs := []any{
			"component", "http",
			"method", request.Method,
			"path", request.URL.Path,
			"status", response.status,
			"bytes", response.bytes,
			"duration_ms", time.Since(started).Milliseconds(),
		}
		if requestID := response.Header().Get(requestIDHeader); requestID != "" {
			attrs = append(attrs, "request_id", requestID)
		}
		slog.Log(request.Context(), level, "http request completed", attrs...)
	})
}
