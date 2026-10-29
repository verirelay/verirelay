package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestErrorSpecsCoverEveryCode(t *testing.T) {
	codes := []errorCode{
		errorNotFound,
		errorNotReady,
		errorMethodNotAllowed,
		errorUnauthorized,
		errorMissingStoreID,
		errorMissingRequestID,
		errorMissingNonce,
		errorInvalidNonce,
		errorReceiptPublicKeyInvalid,
		errorUpstreamRouteInvalid,
		errorRequestBodyReadFailed,
		errorUpstreamNotAllowed,
		errorRequestBodyTooLarge,
		errorRequestIDFailed,
		errorRequestBuildFailed,
		errorUpstreamRequestFailed,
		errorUpstreamResponseTooLarge,
		errorResponseBodyReadFailed,
		errorUpstreamTimeout,
		errorReceiptNotFound,
		errorReceiptStoreCorrupt,
		errorReceiptStoreUnavailable,
		errorAttestationStoreCorrupt,
		errorAttestationStoreUnavailable,
		errorStoreNotFound,
		errorStoreCorrupt,
		errorStoreUnavailable,
		errorAttestationUnavailable,
		errorAttestationBusy,
		errorAttestationDisclosureFailed,
		errorAttestationReportUnsupported,
		errorAttestationReportFailed,
	}
	if len(errorSpecs) != len(codes) {
		t.Fatalf("errorSpecs has %d entries, want %d", len(errorSpecs), len(codes))
	}
	for _, code := range codes {
		if spec, ok := errorSpecs[code]; !ok || spec.status == 0 {
			t.Errorf("error code %q has no non-zero status spec", code)
		}
	}
}

func TestWriteErrorDebugDetailAppendsToMessage(t *testing.T) {
	for _, tc := range []struct {
		name       string
		debug      bool
		wantDetail string
	}{
		{name: "debug enabled", debug: true, wantDetail: ": underlying failure"},
		{name: "debug disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := &Handler{debugErrors: tc.debug}
			recorder := httptest.NewRecorder()
			handler.writeError(recorder, errorUpstreamRequestFailed, "underlying failure")
			if recorder.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
			}
			var response map[string]map[string]any
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			detail := response["error"]
			if detail["code"] != string(errorUpstreamRequestFailed) {
				t.Fatalf("code = %v", detail["code"])
			}
			if got, want := detail["message"], errorSpecs[errorUpstreamRequestFailed].message+tc.wantDetail; got != want {
				t.Fatalf("message = %v, want %q", got, want)
			}
			if _, ok := detail["detail"]; ok {
				t.Fatal("error response contains a detail field")
			}
		})
	}
}
