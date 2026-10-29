package storage

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
)

func TestRedisReceiptStoreContract(t *testing.T) {
	mr := miniredis.RunT(t)
	runReceiptStoreContract(t, receiptStoreContract{
		Name: "redis",
		NewStore: func(t *testing.T, options MemoryReceiptStoreOptions) ReceiptStore {
			mr.FlushAll()
			return newTestRedisReceiptStore(t, mr, t.Name()+":", options.TTL)
		},
		Advance: mr.FastForward,
	})
}

func TestNewRedisClientValidation(t *testing.T) {
	for name, options := range map[string]RedisOptions{
		"empty_addr":  {},
		"negative_db": {Addr: "127.0.0.1:6379", DB: -1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewRedisClient(options); err == nil {
				t.Fatal("NewRedisClient accepted invalid options")
			}
		})
	}
}

func TestRedisReceiptStoreConstructorValidation(t *testing.T) {
	client := newTestRedisClient(t, miniredis.RunT(t))
	for _, test := range []struct {
		name   string
		client *redis.Client
		prefix string
		ttl    time.Duration
	}{
		{name: "nil_client", prefix: "verirelay:", ttl: time.Minute},
		{name: "non_positive_ttl", client: client, prefix: "verirelay:"},
		{name: "empty_key_prefix", client: client, ttl: time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewRedisReceiptStore(test.client, test.prefix, test.ttl); !errors.Is(err, ErrStoreUnavailable) {
				t.Fatalf("NewRedisReceiptStore error = %v", err)
			}
		})
	}
}

func TestRedisStoresKeepKeysUnderThePrefix(t *testing.T) {
	mr := miniredis.RunT(t)
	client := newTestRedisClient(t, mr)
	receipts, err := NewRedisReceiptStore(client, "tenant:", 37*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	attestations, err := NewRedisAttestationStore(client, "tenant:")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := NewRedisRawStore(client, "tenant:")
	if err != nil {
		t.Fatal(err)
	}
	receipt := testSignedReceipt(t, "request-1", 200)
	if err := receipts.Save(context.Background(), receipt.RequestID, receipt); err != nil {
		t.Fatal(err)
	}
	attestation := testArchivedAttestation(t)
	if err := attestations.Save(context.Background(), attestation.KeyID, attestation, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := raw.Save(context.Background(), "store-1", []byte("capture"), time.Minute); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"tenant:receipt:request-1": true, "tenant:attestation:" + attestation.KeyID: true, "tenant:raw:store-1": true}
	keys := mr.Keys()
	if len(keys) != len(want) {
		t.Fatalf("keys = %v", keys)
	}
	for _, key := range keys {
		if !want[key] {
			t.Fatalf("unexpected key %q in %v", key, keys)
		}
	}
	if got := mr.TTL("tenant:receipt:request-1"); got != 37*time.Second {
		t.Fatalf("receipt TTL = %s, want %s", got, 37*time.Second)
	}
	if err := receipts.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
}

func TestRedisReceiptStoreRejectsCorruptRecordsWithoutDeleting(t *testing.T) {
	tests := []struct {
		name string
		data func(t *testing.T) []byte
	}{
		{name: "invalid_json", data: func(*testing.T) []byte { return []byte("{") }},
		{name: "wrong_version", data: func(t *testing.T) []byte {
			return marshalRecord(t, redisReceiptRecord{Version: "wrong"})
		}},
		{name: "mismatched_request_id", data: func(t *testing.T) []byte {
			return marshalRecord(t, redisReceiptRecord{
				Version: redisReceiptRecordVersion, Receipt: testSignedReceipt(t, "another-request", 200),
			})
		}},
		{name: "broken_signature", data: func(t *testing.T) []byte {
			receipt := testSignedReceipt(t, "request-1", 200)
			receipt.Signature.Value = "not-a-valid-signature"
			return marshalRecord(t, redisReceiptRecord{Version: redisReceiptRecordVersion, Receipt: receipt})
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mr := miniredis.RunT(t)
			store := newTestRedisReceiptStore(t, mr, "verirelay:", time.Minute)
			key := store.key("request-1")
			if err := mr.Set(key, string(test.data(t))); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := store.Get(context.Background(), "request-1"); !errors.Is(err, ErrCorruptRecord) || ok {
				t.Fatalf("Get corrupt record = ok:%t err:%v", ok, err)
			}
			if !mr.Exists(key) {
				t.Fatal("corrupt record was deleted")
			}
		})
	}
}

func TestRedisReceiptStoreCanceledContext(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newTestRedisReceiptStore(t, mr, "verirelay:", time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	receipt := testSignedReceipt(t, "request-1", 200)
	if err := store.Save(ctx, receipt.RequestID, receipt); !errors.Is(err, context.Canceled) {
		t.Fatalf("Save error = %v", err)
	}
	if _, _, err := store.Get(ctx, receipt.RequestID); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get error = %v", err)
	}
}

func TestRedisReceiptStoreUnavailable(t *testing.T) {
	mr := miniredis.RunT(t)
	store := newTestRedisReceiptStore(t, mr, "verirelay:", time.Minute)
	mr.Close()
	receipt := testSignedReceipt(t, "request-1", 200)
	if err := store.Save(context.Background(), receipt.RequestID, receipt); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("Save error = %v", err)
	}
	if _, _, err := store.Get(context.Background(), receipt.RequestID); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("Get error = %v", err)
	}
	if err := store.Ready(context.Background()); !errors.Is(err, ErrStoreUnavailable) {
		t.Fatalf("Ready error = %v", err)
	}
}

func newTestRedisClient(t *testing.T, mr *miniredis.Miniredis) *redis.Client {
	t.Helper()
	client, err := NewRedisClient(RedisOptions{
		Addr: mr.Addr(), DialTimeout: 100 * time.Millisecond,
		ReadTimeout: 100 * time.Millisecond, WriteTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func newTestRedisReceiptStore(t *testing.T, mr *miniredis.Miniredis, prefix string, ttl time.Duration) *RedisReceiptStore {
	t.Helper()
	store, err := NewRedisReceiptStore(newTestRedisClient(t, mr), prefix, ttl)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func marshalRecord(t *testing.T, record any) []byte {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
