//go:build integration

package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRedisReceiptStoreIntegration(t *testing.T) {
	runRedisIntegration(t, "redis", os.Getenv("VERIRELAY_TEST_REDIS_ADDR"))
}

func TestValkeyReceiptStoreIntegration(t *testing.T) {
	runRedisIntegration(t, "valkey", os.Getenv("VERIRELAY_TEST_VALKEY_ADDR"))
}

func runRedisIntegration(t *testing.T, name, addr string) {
	t.Helper()
	if addr == "" {
		t.Skipf("%s integration address is not configured", name)
	}
	prefix := fmt.Sprintf("verirelay:test:%s:%d:", name, time.Now().UnixNano())
	newStore := func(t *testing.T, options MemoryReceiptStoreOptions) ReceiptStore {
		t.Helper()
		ttl := options.TTL
		if ttl <= 0 {
			ttl = time.Hour
		}
		store, cleanup := newRedisIntegrationStore(t, addr, prefix+t.Name()+":", ttl)
		t.Cleanup(cleanup)
		return store
	}
	runReceiptStoreContract(t, receiptStoreContract{Name: name, NewStore: newStore, SkipClock: true})
	t.Run(name+"/cross_instance_and_prefix_isolation", func(t *testing.T) {
		storeA, cleanupA := newRedisIntegrationStore(t, addr, prefix+"shared:", time.Hour)
		defer cleanupA()
		storeB, cleanupB := newRedisIntegrationStore(t, addr, prefix+"shared:", time.Hour)
		defer cleanupB()
		isolated, cleanupIsolated := newRedisIntegrationStore(t, addr, prefix+"isolated:", time.Hour)
		defer cleanupIsolated()
		if err := storeA.Save(context.Background(), "req-1", testSignedReceipt(t, "req-1", 200)); err != nil {
			t.Fatal(err)
		}
		if got, ok, err := storeB.Get(context.Background(), "req-1"); err != nil || !ok || got.RequestID != "req-1" {
			t.Fatalf("cross instance Get = %+v ok=%t err=%v", got, ok, err)
		}
		if got, ok, err := isolated.Get(context.Background(), "req-1"); err != nil || ok {
			t.Fatalf("isolated prefix saw receipt = %+v ok=%t err=%v", got, ok, err)
		}
	})
	t.Run(name+"/ready", func(t *testing.T) {
		store, cleanup := newRedisIntegrationStore(t, addr, prefix+"ready:", time.Hour)
		defer cleanup()
		if err := store.Ready(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run(name+"/corrupt_record", func(t *testing.T) {
		store, cleanup := newRedisIntegrationStore(t, addr, prefix+"corrupt:", time.Hour)
		defer cleanup()
		if err := store.client.Set(context.Background(), store.key("req-1"), []byte("{not-json"), time.Hour).Err(); err != nil {
			t.Fatal(err)
		}
		_, _, err := store.Get(context.Background(), "req-1")
		if !errors.Is(err, ErrCorruptRecord) {
			t.Fatalf("corrupt err = %v", err)
		}
		if _, err := store.client.Get(context.Background(), store.key("req-1")).Bytes(); err != nil {
			t.Fatalf("corrupt key was deleted: %v", err)
		}
	})
	t.Run(name+"/unavailable", func(t *testing.T) {
		client, err := NewRedisClient(RedisOptions{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, ReadTimeout: 50 * time.Millisecond, WriteTimeout: 50 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()
		store, err := NewRedisReceiptStore(client, prefix+"down:", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if err := store.Ready(ctx); !errors.Is(err, ErrStoreUnavailable) {
			t.Fatalf("Ready err = %v", err)
		}
	})
}

func newRedisIntegrationStore(t *testing.T, addr, prefix string, ttl time.Duration) (*RedisReceiptStore, func()) {
	t.Helper()
	client, err := NewRedisClient(RedisOptions{Addr: addr, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewRedisReceiptStore(client, prefix, ttl)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var cursor uint64
		for {
			keys, next, err := store.client.Scan(ctx, cursor, prefix+"*", 100).Result()
			if err != nil {
				break
			}
			if len(keys) > 0 {
				_ = store.client.Del(ctx, keys...).Err()
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
		_ = store.client.Close()
	}
	return store, cleanup
}

func TestRedisIntegrationEnvironmentIsExplicit(t *testing.T) {
	for _, name := range []string{"VERIRELAY_TEST_REDIS_ADDR", "VERIRELAY_TEST_VALKEY_ADDR"} {
		if value := os.Getenv(name); value != "" && strings.TrimSpace(value) == "" {
			t.Fatalf("%s is blank", name)
		}
	}
}
