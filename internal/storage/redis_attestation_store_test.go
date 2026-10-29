package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestRedisAttestationStoreSaveGetAndMiss(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newTestRedisAttestationStore(t, mr)
	attestation := testArchivedAttestation(t)
	if err := store.Save(context.Background(), attestation.KeyID, attestation, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(context.Background(), attestation.KeyID)
	if err != nil || !ok || got != attestation {
		t.Fatalf("Get = %+v, ok=%t, err=%v", got, ok, err)
	}
	if got, ok, err := store.Get(context.Background(), "missing"); err != nil || ok || got.KeyID != "" {
		t.Fatalf("Get missing = %+v, ok=%t, err=%v", got, ok, err)
	}
	if err := store.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
}

func TestRedisAttestationStoreRejectsInvalidAndNonPositiveTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newTestRedisAttestationStore(t, mr)
	attestation := testArchivedAttestation(t)
	for _, ttl := range []time.Duration{0, -time.Second} {
		if err := store.Save(context.Background(), attestation.KeyID, attestation, ttl); !errors.Is(err, ErrAttestationStoreUnavailable) {
			t.Fatalf("Save with TTL %s error = %v", ttl, err)
		}
	}
	attestation.KeyID = "mismatched-key-id"
	if err := store.Save(context.Background(), testArchivedAttestation(t).KeyID, attestation, time.Minute); !errors.Is(err, ErrInvalidAttestation) {
		t.Fatalf("Save with mismatched key ID error = %v", err)
	}
}

func TestRedisAttestationStoreTTLExpiry(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newTestRedisAttestationStore(t, mr)
	attestation := testArchivedAttestation(t)
	if err := store.Save(context.Background(), attestation.KeyID, attestation, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	mr.FastForward(3 * time.Second)
	if _, ok, err := store.Get(context.Background(), attestation.KeyID); err != nil || ok {
		t.Fatalf("Get expired = ok:%t err:%v", ok, err)
	}
}

func TestRedisAttestationStoreRejectsCorruptRecords(t *testing.T) {
	tests := []struct {
		name string
		data func(t *testing.T) []byte
	}{
		{name: "invalid_json", data: func(*testing.T) []byte { return []byte("{") }},
		{name: "wrong_version", data: func(t *testing.T) []byte {
			return marshalRecord(t, redisAttestationRecord{Version: "wrong"})
		}},
		{name: "invalid_attestation", data: func(t *testing.T) []byte {
			attestation := testArchivedAttestation(t)
			attestation.KeyID = "mismatched-key-id"
			return marshalRecord(t, redisAttestationRecord{Version: redisAttestationRecordVersion, Attestation: attestation})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			store := newTestRedisAttestationStore(t, mr)
			keyID := testArchivedAttestation(t).KeyID
			key := store.key(keyID)
			if err := mr.Set(key, string(test.data(t))); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := store.Get(context.Background(), keyID); !errors.Is(err, ErrCorruptAttestationRecord) || ok {
				t.Fatalf("Get corrupt record = ok:%t err:%v", ok, err)
			}
			if !mr.Exists(key) {
				t.Fatal("corrupt record was deleted")
			}
		})
	}
}

func TestRedisAttestationStoreConstructorValidation(t *testing.T) {
	if _, err := NewRedisAttestationStore(nil, "verirelay:"); !errors.Is(err, ErrAttestationStoreUnavailable) {
		t.Fatalf("nil client error = %v", err)
	}
	if _, err := NewRedisAttestationStore(newTestRedisClient(t, miniredis.RunT(t)), ""); !errors.Is(err, ErrAttestationStoreUnavailable) {
		t.Fatalf("empty prefix error = %v", err)
	}
}

func TestRedisAttestationStoreCanceledContext(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newTestRedisAttestationStore(t, mr)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attestation := testArchivedAttestation(t)
	if err := store.Save(ctx, attestation.KeyID, attestation, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("Save error = %v", err)
	}
	if _, _, err := store.Get(ctx, attestation.KeyID); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get error = %v", err)
	}
}

func TestRedisAttestationStoreUnavailable(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newTestRedisAttestationStore(t, mr)
	mr.Close()
	attestation := testArchivedAttestation(t)
	if err := store.Save(context.Background(), attestation.KeyID, attestation, time.Minute); !errors.Is(err, ErrAttestationStoreUnavailable) {
		t.Fatalf("Save error = %v", err)
	}
	if _, _, err := store.Get(context.Background(), attestation.KeyID); !errors.Is(err, ErrAttestationStoreUnavailable) {
		t.Fatalf("Get error = %v", err)
	}
	if err := store.Ready(context.Background()); !errors.Is(err, ErrAttestationStoreUnavailable) {
		t.Fatalf("Ready error = %v", err)
	}
}

func newTestRedisAttestationStore(t *testing.T, mr *miniredis.Miniredis) *RedisAttestationStore {
	t.Helper()
	store, err := NewRedisAttestationStore(newTestRedisClient(t, mr), "verirelay:")
	if err != nil {
		t.Fatal(err)
	}
	return store
}
