package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/verirelay/protocol/avr1"
)

// archivedAttestationTTLExtension keeps an archived attestation response
// alive longer than the Receipts that reference it.
const archivedAttestationTTLExtension = 30 * time.Minute

var (
	ErrAttestationStoreUnavailable = errors.New("attestation store unavailable")
	ErrCorruptAttestationRecord    = errors.New("corrupt attestation record")
	ErrInvalidAttestation          = errors.New("invalid attestation")
)

// ArchivedAttestationTTL returns the retention of the archived attestation
// response for a Receipt store whose Receipts live for receiptTTL.
func ArchivedAttestationTTL(receiptTTL time.Duration) time.Duration {
	if receiptTTL > math.MaxInt64-archivedAttestationTTLExtension {
		return math.MaxInt64
	}
	return receiptTTL + archivedAttestationTTLExtension
}

type AttestationStore interface {
	Save(ctx context.Context, keyID string, attestation avr1.AttestationResponse, ttl time.Duration) error
	Get(ctx context.Context, keyID string) (avr1.AttestationResponse, bool, error)
}

type MemoryAttestationStoreOptions struct {
	Now func() time.Time
}

// MemoryAttestationStore keeps the archived attestation response in process
// memory. A process archives only its own Receipt Signing Key, so it holds one
// entry.
type MemoryAttestationStore struct {
	mu           sync.Mutex
	attestations ttlMap[[]byte]
	now          func() time.Time
}

func NewMemoryAttestationStore() *MemoryAttestationStore {
	return NewMemoryAttestationStoreWithOptions(MemoryAttestationStoreOptions{})
}

func NewMemoryAttestationStoreWithOptions(options MemoryAttestationStoreOptions) *MemoryAttestationStore {
	if options.Now == nil {
		options.Now = time.Now
	}
	return &MemoryAttestationStore{attestations: newTTLMap(1, countEntries[[]byte]), now: options.Now}
}

func validateAttestation(keyID string, attestation avr1.AttestationResponse) error {
	if keyID == "" || attestation.KeyID != keyID || attestation.Version != avr1.AVRVersion {
		return ErrInvalidAttestation
	}
	publicKey, err := avr1.DecodeEd25519PublicKeyBase64URL(attestation.ReceiptSigningPublicKey)
	if err != nil || avr1.ReceiptKeyID(publicKey) != keyID {
		return ErrInvalidAttestation
	}
	if _, err := avr1.DecodeNonceBase64URL(attestation.Nonce); err != nil {
		return ErrInvalidAttestation
	}
	return nil
}

func (s *MemoryAttestationStore) Save(ctx context.Context, keyID string, attestation avr1.AttestationResponse, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ttl <= 0 {
		return fmt.Errorf("%w: ttl must be positive", ErrAttestationStoreUnavailable)
	}
	if err := validateAttestation(keyID, attestation); err != nil {
		return err
	}
	data, err := json.Marshal(attestation)
	if err != nil {
		return fmt.Errorf("%w: marshal attestation: %w", ErrInvalidAttestation, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attestations.put(s.now(), keyID, data, ttl)
	return nil
}

func (s *MemoryAttestationStore) Get(ctx context.Context, keyID string) (avr1.AttestationResponse, bool, error) {
	if err := ctx.Err(); err != nil {
		return avr1.AttestationResponse{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.attestations.get(keyID, s.now)
	if !ok {
		return avr1.AttestationResponse{}, false, nil
	}
	var attestation avr1.AttestationResponse
	if err := json.Unmarshal(data, &attestation); err != nil {
		return avr1.AttestationResponse{}, false, fmt.Errorf("%w: invalid json", ErrCorruptAttestationRecord)
	}
	if err := validateAttestation(keyID, attestation); err != nil {
		return avr1.AttestationResponse{}, false, fmt.Errorf("%w: invalid attestation", ErrCorruptAttestationRecord)
	}
	return attestation, true, nil
}

func (s *MemoryAttestationStore) Ready(context.Context) error { return nil }
