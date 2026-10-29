package storage

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/verirelay/protocol/avr1"
	"github.com/verirelay/protocol/veriprov1"
)

// Memory Receipt store defaults, the Runtime Config defaults of receipt_store.
const (
	DefaultReceiptStoreTTL        = veriprov1.DefaultReceiptStoreTTLSeconds * time.Second
	DefaultReceiptStoreMaxEntries = veriprov1.DefaultReceiptStoreMaxEntries
)

var (
	ErrStoreUnavailable         = errors.New("receipt store unavailable")
	ErrCorruptRecord            = errors.New("corrupt receipt record")
	ErrReceiptRequestIDMismatch = errors.New("receipt request ID mismatch")
	ErrInvalidReceipt           = errors.New("invalid receipt")
)

type ReceiptStore interface {
	Save(ctx context.Context, requestID string, receipt avr1.SignedReceiptV1) error
	Get(ctx context.Context, requestID string) (avr1.SignedReceiptV1, bool, error)
}

type ReadinessChecker interface {
	Ready(ctx context.Context) error
}

// MemoryReceiptStoreOptions configures a MemoryReceiptStore. A non-positive
// TTL or MaxEntries takes its default.
type MemoryReceiptStoreOptions struct {
	TTL        time.Duration
	MaxEntries int
	Now        func() time.Time
}

// MemoryReceiptStore keeps Receipts in process memory for TTL, evicting the
// oldest Receipt beyond MaxEntries.
type MemoryReceiptStore struct {
	mu       sync.Mutex
	receipts ttlMap[avr1.SignedReceiptV1]
	ttl      time.Duration
	now      func() time.Time
}

func NewMemoryReceiptStore() *MemoryReceiptStore {
	return NewMemoryReceiptStoreWithOptions(MemoryReceiptStoreOptions{})
}

func NewMemoryReceiptStoreWithOptions(options MemoryReceiptStoreOptions) *MemoryReceiptStore {
	if options.TTL <= 0 {
		options.TTL = DefaultReceiptStoreTTL
	}
	if options.MaxEntries <= 0 {
		options.MaxEntries = DefaultReceiptStoreMaxEntries
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &MemoryReceiptStore{
		receipts: newTTLMap(int64(options.MaxEntries), countEntries[avr1.SignedReceiptV1]),
		ttl:      options.TTL,
		now:      options.Now,
	}
}

func validateReceipt(requestID string, receipt avr1.SignedReceiptV1) error {
	if requestID == "" || receipt.RequestID != requestID {
		return ErrReceiptRequestIDMismatch
	}
	if receipt.Signature == nil || avr1.VerifySignedReceiptV1(receipt) != nil {
		return ErrInvalidReceipt
	}
	return nil
}

// Save atomically publishes a final signed receipt. A failed local validation never publishes it.
func (s *MemoryReceiptStore) Save(ctx context.Context, requestID string, receipt avr1.SignedReceiptV1) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateReceipt(requestID, receipt); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.receipts.put(s.now(), requestID, cloneSignedReceiptV1(receipt), s.ttl)
	return nil
}

func (s *MemoryReceiptStore) Get(ctx context.Context, requestID string) (avr1.SignedReceiptV1, bool, error) {
	if err := ctx.Err(); err != nil {
		return avr1.SignedReceiptV1{}, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, ok := s.receipts.get(requestID, s.now)
	if !ok {
		return avr1.SignedReceiptV1{}, false, nil
	}
	return cloneSignedReceiptV1(receipt), true, nil
}

func (s *MemoryReceiptStore) Ready(context.Context) error { return nil }

func cloneSignedReceiptV1(receipt avr1.SignedReceiptV1) avr1.SignedReceiptV1 {
	clone := receipt
	if receipt.Signature != nil {
		signature := *receipt.Signature
		clone.Signature = &signature
	}
	return clone
}
