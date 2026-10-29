package storage

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func newTestMemoryRawStore() *MemoryRawStore {
	return NewMemoryRawStoreWithOptions(MemoryRawStoreOptions{})
}

func TestMemoryRawStoreSaveGetAndCopies(t *testing.T) {
	store := newTestMemoryRawStore()
	input := []byte("original")
	if err := store.Save(context.Background(), "store-1", input, time.Second); err != nil {
		t.Fatal(err)
	}
	input[0] = 'X'
	got, ok, err := store.Get(context.Background(), "store-1")
	if err != nil || !ok || !bytes.Equal(got, []byte("original")) {
		t.Fatalf("Get = %q, ok=%t, err=%v", got, ok, err)
	}
	got[0] = 'Y'
	again, ok, err := store.Get(context.Background(), "store-1")
	if err != nil || !ok || !bytes.Equal(again, []byte("original")) {
		t.Fatalf("Get after mutating returned data = %q, ok=%t, err=%v", again, ok, err)
	}
}

func TestMemoryRawStoreDefaultsToRuntimeConfigMaxBytes(t *testing.T) {
	store := NewMemoryRawStoreWithOptions(MemoryRawStoreOptions{MaxBytes: -1})
	if store.records.limit != 256<<20 {
		t.Fatalf("limit = %d, want 256 MiB", store.records.limit)
	}
}

func TestMemoryRawStoreEvictsOldestUntilTheRecordFits(t *testing.T) {
	store := NewMemoryRawStoreWithOptions(MemoryRawStoreOptions{MaxBytes: 10})
	ctx := context.Background()
	for _, id := range []string{"a", "b", "c"} {
		if err := store.Save(ctx, id, []byte("123"), time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Save(ctx, "d", []byte("12345"), time.Hour); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"a": false, "b": false, "c": true, "d": true} {
		if _, ok, _ := store.Get(ctx, id); ok != want {
			t.Fatalf("record %s present = %t, want %t", id, ok, want)
		}
	}
	if store.records.used != 8 {
		t.Fatalf("used = %d, want 8", store.records.used)
	}
}

func TestMemoryRawStoreReplacingARecordFreesItsBytes(t *testing.T) {
	store := NewMemoryRawStoreWithOptions(MemoryRawStoreOptions{MaxBytes: 10})
	ctx := context.Background()
	for _, data := range []string{"1234567", "12345678"} {
		if err := store.Save(ctx, "a", []byte(data), time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Save(ctx, "b", []byte("12"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get(ctx, "a"); !ok {
		t.Fatal("replaced record was evicted")
	}
	if store.records.used != 10 {
		t.Fatalf("used = %d, want 10", store.records.used)
	}
}

func TestMemoryRawStoreRejectsARecordLargerThanMaxBytes(t *testing.T) {
	store := NewMemoryRawStoreWithOptions(MemoryRawStoreOptions{MaxBytes: 4})
	ctx := context.Background()
	if err := store.Save(ctx, "small", []byte("1234"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, "large", []byte("12345"), time.Hour); !errors.Is(err, ErrRawRecordTooLarge) {
		t.Fatalf("Save error = %v", err)
	}
	if _, ok, _ := store.Get(ctx, "large"); ok {
		t.Fatal("oversized record was stored")
	}
	if _, ok, _ := store.Get(ctx, "small"); !ok {
		t.Fatal("oversized record evicted an existing one")
	}
}

func TestMemoryRawStoreMissAndDelete(t *testing.T) {
	store := newTestMemoryRawStore()
	if got, ok, err := store.Get(context.Background(), "missing"); err != nil || ok || got != nil {
		t.Fatalf("Get missing = %q, ok=%t, err=%v", got, ok, err)
	}
	if err := store.Save(context.Background(), "store-1", []byte("value"), time.Second); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(context.Background(), "store-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.Get(context.Background(), "store-1"); err != nil || ok {
		t.Fatalf("Get after Delete = ok:%t err:%v", ok, err)
	}
	if store.records.used != 0 {
		t.Fatalf("used after Delete = %d", store.records.used)
	}
}

func TestMemoryRawStoreExpiresAtExactBoundaryAndDeletes(t *testing.T) {
	now := time.Date(2026, time.July, 12, 7, 0, 0, 0, time.UTC)
	store := NewMemoryRawStoreWithOptions(MemoryRawStoreOptions{Now: func() time.Time { return now }})
	if err := store.Save(context.Background(), "store-1", []byte("value"), time.Second); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if got, ok, err := store.Get(context.Background(), "store-1"); err != nil || ok || got != nil {
		t.Fatalf("Get at expiry = %q, ok=%t, err=%v", got, ok, err)
	}
	store.mu.Lock()
	_, exists := store.records.entries["store-1"]
	used := store.records.used
	store.mu.Unlock()
	if exists || used != 0 {
		t.Fatalf("expired entry was not removed: exists=%t used=%d", exists, used)
	}
}

func TestMemoryRawStoreRejectsInvalidOptions(t *testing.T) {
	store := newTestMemoryRawStore()
	for _, test := range []struct {
		name    string
		storeID string
		ttl     time.Duration
	}{
		{name: "empty_store_id", storeID: "", ttl: time.Second},
		{name: "subsecond_ttl", storeID: "store-1", ttl: time.Second - time.Nanosecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := store.Save(context.Background(), test.storeID, []byte("value"), test.ttl); !errors.Is(err, ErrRawStoreUnavailable) {
				t.Fatalf("Save error = %v", err)
			}
		})
	}
}

func TestMemoryRawStoreCanceledContext(t *testing.T) {
	store := newTestMemoryRawStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Save(ctx, "store-1", []byte("value"), time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Save error = %v", err)
	}
	if _, _, err := store.Get(ctx, "store-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get error = %v", err)
	}
	if err := store.Delete(ctx, "store-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete error = %v", err)
	}
}
