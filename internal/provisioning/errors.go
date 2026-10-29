package provisioning

import "errors"

var (
	ErrUnavailable       = errors.New("provisioning unavailable")
	ErrInvalidResponse   = errors.New("invalid provisioning response")
	ErrReportUnavailable = errors.New("attestation report unavailable")
	ErrEnvelopeSignature = errors.New("provision envelope signature rejected")
	ErrHPKEOpen          = errors.New("runtime config decryption failed")
	ErrInvalidBootstrap  = errors.New("invalid bootstrap")
	ErrHostDataMismatch  = errors.New("bootstrap does not match HOST_DATA")
)
