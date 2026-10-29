package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/verirelay/protocol/veriprov1"
)

// DefaultRawStoreMaxBytes bounds the memory Raw Capture store, the Runtime
// Config default of raw_store.max_bytes.
const DefaultRawStoreMaxBytes int64 = veriprov1.DefaultRawStoreMaxBytes

var (
	ErrRawStoreUnavailable = errors.New("raw store unavailable")
	ErrRawStoreCorrupt     = errors.New("corrupt raw store record")
	ErrRawRecordTooLarge   = errors.New("raw capture exceeds the memory store size")
)

type RawStore interface {
	Save(ctx context.Context, storeID string, data []byte, ttl time.Duration) error
	Get(ctx context.Context, storeID string) ([]byte, bool, error)
}

type RawStoreDeleter interface {
	Delete(ctx context.Context, storeID string) error
}

// MemoryRawStoreOptions configures a MemoryRawStore. A non-positive MaxBytes
// takes DefaultRawStoreMaxBytes.
type MemoryRawStoreOptions struct {
	MaxBytes int64
	Now      func() time.Time
}

// MemoryRawStore keeps Raw Captures in process memory, bounded by the total
// size of the stored records: the oldest records are evicted until a new one
// fits, and a record larger than MaxBytes is not stored.
type MemoryRawStore struct {
	mu      sync.Mutex
	records ttlMap[[]byte]
	now     func() time.Time
}

func NewMemoryRawStoreWithOptions(options MemoryRawStoreOptions) *MemoryRawStore {
	if options.MaxBytes <= 0 {
		options.MaxBytes = DefaultRawStoreMaxBytes
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &MemoryRawStore{records: newTTLMap(options.MaxBytes, recordSize), now: options.Now}
}

func recordSize(data []byte) int64 { return int64(len(data)) }

func (s *MemoryRawStore) Save(ctx context.Context, storeID string, data []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if storeID == "" || ttl < time.Second {
		return ErrRawStoreUnavailable
	}
	copied := append([]byte(nil), data...)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.records.put(s.now(), storeID, copied, ttl) {
		return fmt.Errorf("%w: %d bytes, limit %d", ErrRawRecordTooLarge, len(data), s.records.limit)
	}
	return nil
}

func (s *MemoryRawStore) Get(ctx context.Context, storeID string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.records.get(storeID, s.now)
	if !ok {
		return nil, false, nil
	}
	return append([]byte(nil), data...), true, nil
}

func (s *MemoryRawStore) Delete(ctx context.Context, storeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records.remove(storeID)
	return nil
}
