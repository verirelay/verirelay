package storage

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

const testRawKey = "verirelay:raw:payload-1"

func TestRedisRawStoreSaveGetDelete(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newTestRedisRawStore(t, mr)
	data := []byte("raw payload")
	if err := store.Save(context.Background(), "payload-1", data, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(context.Background(), "payload-1")
	if err != nil || !ok || !bytes.Equal(got, data) {
		t.Fatalf("Get = %q, ok=%t, err=%v", got, ok, err)
	}
	if got, ok, err := store.Get(context.Background(), "missing"); err != nil || ok || got != nil {
		t.Fatalf("Get missing = %q, ok=%t, err=%v", got, ok, err)
	}
	if err := store.Delete(context.Background(), "payload-1"); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := store.Get(context.Background(), "payload-1"); err != nil || ok || got != nil {
		t.Fatalf("Get after Delete = %q, ok=%t, err=%v", got, ok, err)
	}
}

func TestRedisRawStoreUsesTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newTestRedisRawStore(t, mr)
	if err := store.Save(context.Background(), "payload-1", []byte("value"), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if got := mr.TTL(testRawKey); got != 2*time.Second {
		t.Fatalf("TTL = %s, want %s", got, 2*time.Second)
	}
	mr.FastForward(2 * time.Second)
	if mr.Exists(testRawKey) {
		t.Fatal("key remains after TTL expiry")
	}
	if err := store.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
}

func TestRedisRawStoreTruncatesFractionalTTLToSeconds(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newTestRedisRawStore(t, mr)
	if err := store.Save(context.Background(), "payload-1", []byte("value"), 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := mr.TTL(testRawKey); got != time.Second {
		t.Fatalf("TTL = %s, want %s", got, time.Second)
	}
}

func TestRedisRawStoreRejectsSubsecondTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newTestRedisRawStore(t, mr)
	err := store.Save(context.Background(), "payload-1", []byte("value"), time.Second-time.Nanosecond)
	if !errors.Is(err, ErrRawStoreUnavailable) {
		t.Fatalf("Save error = %v", err)
	}
	if mr.Exists(testRawKey) {
		t.Fatal("subsecond record was stored")
	}
}

func TestRedisRawStoreConstructorValidation(t *testing.T) {
	if _, err := NewRedisRawStore(nil, "verirelay:"); !errors.Is(err, ErrRawStoreUnavailable) {
		t.Fatalf("nil client error = %v", err)
	}
	if _, err := NewRedisRawStore(newTestRedisClient(t, miniredis.RunT(t)), ""); !errors.Is(err, ErrRawStoreUnavailable) {
		t.Fatalf("empty prefix error = %v", err)
	}
}

func TestRedisRawStoreCanceledContext(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newTestRedisRawStore(t, mr)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Save(ctx, "payload-1", []byte("value"), time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("Save error = %v", err)
	}
	if _, _, err := store.Get(ctx, "payload-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get error = %v", err)
	}
	if err := store.Delete(ctx, "payload-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete error = %v", err)
	}
}

func TestRedisRawStoreUnavailable(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newTestRedisRawStore(t, mr)
	mr.Close()
	if err := store.Save(context.Background(), "payload-1", []byte("value"), time.Second); !errors.Is(err, ErrRawStoreUnavailable) {
		t.Fatalf("Save error = %v", err)
	}
	if _, _, err := store.Get(context.Background(), "payload-1"); !errors.Is(err, ErrRawStoreUnavailable) {
		t.Fatalf("Get error = %v", err)
	}
	if err := store.Delete(context.Background(), "payload-1"); !errors.Is(err, ErrRawStoreUnavailable) {
		t.Fatalf("Delete error = %v", err)
	}
	if err := store.Ready(context.Background()); !errors.Is(err, ErrRawStoreUnavailable) {
		t.Fatalf("Ready error = %v", err)
	}
}

func newTestRedisRawStore(t *testing.T, mr *miniredis.Miniredis) *RedisRawStore {
	t.Helper()
	store, err := NewRedisRawStore(newTestRedisClient(t, mr), "verirelay:")
	if err != nil {
		t.Fatal(err)
	}
	return store
}
