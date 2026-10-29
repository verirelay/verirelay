package proxy

import "net/http"

type errorCode string

const (
	errorNotFound                     errorCode = "not_found"
	errorNotReady                     errorCode = "not_ready"
	errorMethodNotAllowed             errorCode = "method_not_allowed"
	errorUnauthorized                 errorCode = "unauthorized"
	errorMissingStoreID               errorCode = "missing_store_id"
	errorMissingRequestID             errorCode = "missing_request_id"
	errorMissingNonce                 errorCode = "missing_nonce"
	errorInvalidNonce                 errorCode = "invalid_nonce"
	errorReceiptPublicKeyInvalid      errorCode = "receipt_public_key_invalid"
	errorUpstreamRouteInvalid         errorCode = "upstream_route_invalid"
	errorRequestBodyReadFailed        errorCode = "request_body_read_failed"
	errorUpstreamNotAllowed           errorCode = "upstream_not_allowed"
	errorRequestBodyTooLarge          errorCode = "request_body_too_large"
	errorRequestIDFailed              errorCode = "request_id_failed"
	errorRequestBuildFailed           errorCode = "request_build_failed"
	errorUpstreamRequestFailed        errorCode = "upstream_request_failed"
	errorUpstreamResponseTooLarge     errorCode = "upstream_response_too_large"
	errorResponseBodyReadFailed       errorCode = "response_body_read_failed"
	errorUpstreamTimeout              errorCode = "upstream_timeout"
	errorReceiptNotFound              errorCode = "receipt_not_found"
	errorReceiptStoreCorrupt          errorCode = "receipt_store_corrupt"
	errorReceiptStoreUnavailable      errorCode = "receipt_store_unavailable"
	errorAttestationStoreCorrupt      errorCode = "attestation_store_corrupt"
	errorAttestationStoreUnavailable  errorCode = "attestation_store_unavailable"
	errorStoreNotFound                errorCode = "store_not_found"
	errorStoreCorrupt                 errorCode = "store_corrupt"
	errorStoreUnavailable             errorCode = "store_unavailable"
	errorAttestationUnavailable       errorCode = "attestation_unavailable"
	errorAttestationBusy              errorCode = "attestation_busy"
	errorAttestationDisclosureFailed  errorCode = "attestation_disclosure_failed"
	errorAttestationReportUnsupported errorCode = "attestation_report_unsupported"
	errorAttestationReportFailed      errorCode = "attestation_report_failed"
)

type errorSpec struct {
	status  int
	message string
}

var errorSpecs = map[errorCode]errorSpec{
	errorNotFound:                     {http.StatusNotFound, "route not found"},
	errorNotReady:                     {http.StatusServiceUnavailable, "service is not ready"},
	errorMethodNotAllowed:             {http.StatusMethodNotAllowed, "method not allowed"},
	errorUnauthorized:                 {http.StatusUnauthorized, "authorization required"},
	errorMissingStoreID:               {http.StatusBadRequest, "store id is required"},
	errorMissingRequestID:             {http.StatusBadRequest, "request id is required"},
	errorMissingNonce:                 {http.StatusBadRequest, "nonce is required"},
	errorInvalidNonce:                 {http.StatusBadRequest, "nonce is invalid"},
	errorReceiptPublicKeyInvalid:      {http.StatusInternalServerError, "receipt public key is invalid"},
	errorUpstreamRouteInvalid:         {http.StatusBadRequest, "upstream route is invalid"},
	errorRequestBodyReadFailed:        {http.StatusBadRequest, "request body could not be read"},
	errorUpstreamNotAllowed:           {http.StatusForbidden, "upstream is not allowed"},
	errorRequestBodyTooLarge:          {http.StatusRequestEntityTooLarge, "request body exceeds the maximum size"},
	errorRequestIDFailed:              {http.StatusInternalServerError, "request id could not be generated"},
	errorRequestBuildFailed:           {http.StatusInternalServerError, "upstream request could not be built"},
	errorUpstreamRequestFailed:        {http.StatusBadGateway, "upstream request failed"},
	errorUpstreamResponseTooLarge:     {http.StatusBadGateway, "upstream response exceeds the maximum size"},
	errorResponseBodyReadFailed:       {http.StatusBadGateway, "upstream response body could not be read"},
	errorUpstreamTimeout:              {http.StatusGatewayTimeout, "upstream request timed out"},
	errorReceiptNotFound:              {http.StatusNotFound, "receipt was not found"},
	errorReceiptStoreCorrupt:          {http.StatusInternalServerError, "receipt store record is corrupt"},
	errorReceiptStoreUnavailable:      {http.StatusServiceUnavailable, "receipt store is unavailable"},
	errorAttestationStoreCorrupt:      {http.StatusInternalServerError, "attestation store record is corrupt"},
	errorAttestationStoreUnavailable:  {http.StatusServiceUnavailable, "attestation store is unavailable"},
	errorStoreNotFound:                {http.StatusNotFound, "store record was not found"},
	errorStoreCorrupt:                 {http.StatusInternalServerError, "store record is corrupt"},
	errorStoreUnavailable:             {http.StatusServiceUnavailable, "store is unavailable"},
	errorAttestationUnavailable:       {http.StatusServiceUnavailable, "attestation is unavailable"},
	errorAttestationBusy:              {http.StatusServiceUnavailable, "attestation is busy"},
	errorAttestationDisclosureFailed:  {http.StatusInternalServerError, "attestation disclosure failed"},
	errorAttestationReportUnsupported: {http.StatusInternalServerError, "attestation report signer is unsupported"},
	errorAttestationReportFailed:      {http.StatusBadGateway, "attestation report failed"},
}
