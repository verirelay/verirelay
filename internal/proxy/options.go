package proxy

import (
	"time"

	"github.com/verirelay/protocol/avr1"
	"github.com/verirelay/verirelay/internal/attestation"
	"github.com/verirelay/verirelay/internal/storage"
)

type Options struct {
	AllowPrivateUpstreams bool
	// AdminToken gates the forwarding route through X-VeriRelay-Token.
	AdminToken string
	// AttestationManager serves the explicit /v1/avr/attestation endpoint only.
	AttestationManager     *attestation.Manager
	ReceiptSigner          *avr1.ReceiptSignerV1
	ReceiptStore           storage.ReceiptStore
	AttestationStore       storage.AttestationStore
	ArchivedAttestation    *avr1.AttestationResponse
	ArchivedAttestationTTL time.Duration
	RawStore               storage.RawStore
	// RawStoreMaxTTL caps the retention a client may request; 0 means no cap.
	RawStoreMaxTTL time.Duration
}
