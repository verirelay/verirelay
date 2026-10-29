package storage

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/verirelay/protocol/avr1"
)

func testSignedReceipt(t *testing.T, requestID string, status int) avr1.SignedReceiptV1 {
	t.Helper()
	signer, err := avr1.NewReceiptSignerV1(ed25519.NewKeyFromSeed([]byte("0123456789abcdef0123456789abcdef")))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := avr1.BuildSignedReceiptV1(avr1.ReceiptFactsV1{
		RequestID: requestID, Timestamp: "2026-07-11T07:00:00Z", Method: "POST", UpstreamAuthority: "api.example.com", UpstreamPath: "/v1/messages", SNI: "api.example.com",
		UpstreamSPKISHA256: avr1.SHA256Hex([]byte("spki")), RequestBodySHA256: avr1.SHA256Hex([]byte("request")), ResponseBodySHA256: avr1.SHA256Hex(fmt.Appendf(nil, "response-%d", status)), ResponseStatus: status,
	}, signer)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func TestMemoryReceiptStoreContract(t *testing.T) {
	now := time.Date(2026, time.July, 12, 7, 0, 0, 0, time.UTC)
	runReceiptStoreContract(t, receiptStoreContract{
		Name: "memory",
		NewStore: func(t *testing.T, options MemoryReceiptStoreOptions) ReceiptStore {
			options.Now = func() time.Time { return now }
			return NewMemoryReceiptStoreWithOptions(options)
		},
		Advance: func(d time.Duration) { now = now.Add(d) },
	})
}

const httpStatusCreated = 201

func TestMemoryReceiptStoreExpiresAndDeletesOnGet(t *testing.T) {
	now := time.Date(2026, time.July, 12, 7, 0, 0, 0, time.UTC)
	store := NewMemoryReceiptStoreWithOptions(MemoryReceiptStoreOptions{TTL: time.Minute, Now: func() time.Time { return now }})
	if err := store.Save(context.Background(), "expired", testSignedReceipt(t, "expired", 200)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if got, ok, err := store.Get(context.Background(), "expired"); err != nil || ok || got != (avr1.SignedReceiptV1{}) {
		t.Fatalf("Get(expired) = %+v, %t, %v", got, ok, err)
	}
	if _, exists := store.receipts.entries["expired"]; exists {
		t.Fatal("expired receipt was not deleted")
	}
}

func TestMemoryReceiptStoreEvictsOldestSave(t *testing.T) {
	store := NewMemoryReceiptStoreWithOptions(MemoryReceiptStoreOptions{MaxEntries: 2})
	for _, requestID := range []string{"a", "b", "c"} {
		if err := store.Save(context.Background(), requestID, testSignedReceipt(t, requestID, 200)); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, _ := store.Get(context.Background(), "a"); ok {
		t.Fatal("oldest receipt was not evicted")
	}
	for _, requestID := range []string{"b", "c"} {
		if _, ok, _ := store.Get(context.Background(), requestID); !ok {
			t.Fatalf("expected %q to remain", requestID)
		}
	}
}

func TestMemoryReceiptStoreRemovesExpiredBeforeEviction(t *testing.T) {
	now := time.Date(2026, time.July, 12, 7, 0, 0, 0, time.UTC)
	store := NewMemoryReceiptStoreWithOptions(MemoryReceiptStoreOptions{TTL: time.Minute, MaxEntries: 2, Now: func() time.Time { return now }})
	if err := store.Save(context.Background(), "expired", testSignedReceipt(t, "expired", 200)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	if err := store.Save(context.Background(), "active", testSignedReceipt(t, "active", 200)); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Second)
	if err := store.Save(context.Background(), "new", testSignedReceipt(t, "new", 200)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get(context.Background(), "expired"); ok {
		t.Fatal("expired receipt remained")
	}
	for _, requestID := range []string{"active", "new"} {
		if _, ok, _ := store.Get(context.Background(), requestID); !ok {
			t.Fatalf("expected %q to remain", requestID)
		}
	}
}

func TestMemoryReceiptStoreDefaultsInvalidOptions(t *testing.T) {
	store := NewMemoryReceiptStoreWithOptions(MemoryReceiptStoreOptions{TTL: -time.Second, MaxEntries: -1})
	if store.ttl != time.Hour || store.receipts.limit != 10000 || store.now == nil {
		t.Fatalf("unexpected defaults: ttl=%s max_entries=%d has_now=%t", store.ttl, store.receipts.limit, store.now != nil)
	}
}

func TestMemoryReceiptStoreConcurrentReadWrite(t *testing.T) {
	store := NewMemoryReceiptStoreWithOptions(MemoryReceiptStoreOptions{MaxEntries: 128})
	const workers = 64
	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			requestID := fmt.Sprintf("req-%02d", i)
			if err := store.Save(context.Background(), requestID, testSignedReceipt(t, requestID, 200+i)); err != nil {
				t.Errorf("Save(%q): %v", requestID, err)
				return
			}
			if receipt, ok, err := store.Get(context.Background(), requestID); err != nil || !ok || receipt.RequestID != requestID {
				t.Errorf("Get(%q) = %+v, %t, %v", requestID, receipt, ok, err)
			}
		})
	}
	wg.Wait()
}
