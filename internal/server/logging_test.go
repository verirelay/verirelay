package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoggingMiddlewareRecordsSafeRequestFields(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(previous)

	handler := LoggingMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set(requestIDHeader, "req_test")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte("ok"))
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/resource?token=secret", nil))

	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record["path"] != "/resource" || record["status"] != float64(http.StatusCreated) || record["request_id"] != "req_test" {
		t.Fatalf("unexpected log record: %+v", record)
	}
	if strings.Contains(output.String(), "token=secret") {
		t.Fatalf("query leaked into log: %s", output.String())
	}
}

func TestLoggingMiddlewarePreservesFlusher(t *testing.T) {
	flushed := false
	handler := LoggingMiddleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		flusher, ok := writer.(http.Flusher)
		if !ok {
			t.Error("wrapped response writer does not implement http.Flusher")
			return
		}
		_, _ = writer.Write([]byte("data: one\n\n"))
		flusher.Flush()
		flushed = true
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/stream", nil))
	if !flushed || response.Body.String() != "data: one\n\n" {
		t.Fatalf("stream response changed: flushed=%t body=%q", flushed, response.Body.String())
	}
}
