package server

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/verirelay/verirelay/internal/config"
	"github.com/verirelay/verirelay/internal/storage"
)

func TestNewStores(t *testing.T) {
	redisA := config.RedisConfig{Addr: "127.0.0.1:6379", DialTimeout: time.Second}
	redisB := config.RedisConfig{Addr: "127.0.0.1:6380", DialTimeout: time.Second}
	memoryReceipts := config.ReceiptStoreConfig{Type: config.StoreMemory, TTL: time.Hour, MaxEntries: 10}
	redisReceipts := config.ReceiptStoreConfig{Type: config.StoreRedis, TTL: time.Hour, KeyPrefix: "verirelay:", Redis: redisA}
	for _, tc := range []struct {
		name                   string
		receipts               config.ReceiptStoreConfig
		raw                    config.RawStoreConfig
		wantReceipts, wantRaw  any
		wantClients            int
		wantErr                error
		wantNilRaw, wantShared bool
	}{
		{name: "memory", receipts: memoryReceipts, raw: config.RawStoreConfig{Type: config.StoreMemory, MaxBytes: 1024}, wantReceipts: &storage.MemoryReceiptStore{}, wantRaw: &storage.MemoryRawStore{}},
		{name: "raw disabled", receipts: memoryReceipts, raw: config.RawStoreConfig{Type: config.RawStoreDisabled}, wantReceipts: &storage.MemoryReceiptStore{}, wantNilRaw: true},
		{name: "shared redis", receipts: redisReceipts, raw: config.RawStoreConfig{Type: config.StoreRedis, KeyPrefix: "verirelay:", Redis: redisA}, wantReceipts: &storage.RedisReceiptStore{}, wantRaw: &storage.RedisRawStore{}, wantClients: 1},
		{name: "separate redis", receipts: redisReceipts, raw: config.RawStoreConfig{Type: config.StoreRedis, KeyPrefix: "raw:", Redis: redisB}, wantReceipts: &storage.RedisReceiptStore{}, wantRaw: &storage.RedisRawStore{}, wantClients: 2},
		{name: "redis raw with memory receipts", receipts: memoryReceipts, raw: config.RawStoreConfig{Type: config.StoreRedis, KeyPrefix: "verirelay:", Redis: redisA}, wantReceipts: &storage.MemoryReceiptStore{}, wantRaw: &storage.RedisRawStore{}, wantClients: 1},
		{name: "bogus receipt type", receipts: config.ReceiptStoreConfig{Type: "bogus"}, wantErr: config.ErrInvalidReceiptStore},
		{name: "bogus raw type", receipts: memoryReceipts, raw: config.RawStoreConfig{Type: "bogus"}, wantErr: config.ErrInvalidRawStore},
		{name: "redis without addr", receipts: config.ReceiptStoreConfig{Type: config.StoreRedis, TTL: time.Hour, KeyPrefix: "verirelay:"}, wantErr: config.ErrInvalidReceiptStore},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stores, err := newStores(config.Config{ReceiptStore: tc.receipts, RawStore: tc.raw})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("newStores error = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer stores.close()
			if got, want := fmt.Sprintf("%T", stores.receipts), fmt.Sprintf("%T", tc.wantReceipts); got != want {
				t.Fatalf("receipt store = %s, want %s", got, want)
			}
			if tc.wantNilRaw {
				if stores.raw != nil {
					t.Fatalf("raw store = %T, want none", stores.raw)
				}
			} else if got, want := fmt.Sprintf("%T", stores.raw), fmt.Sprintf("%T", tc.wantRaw); got != want {
				t.Fatalf("raw store = %s, want %s", got, want)
			}
			if len(stores.clients) != tc.wantClients {
				t.Fatalf("redis clients = %d, want %d", len(stores.clients), tc.wantClients)
			}
		})
	}
}
