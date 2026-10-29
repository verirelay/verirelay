package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/verirelay/protocol/avr1"
)

type receiptStoreContract struct {
	Name      string
	NewStore  func(t *testing.T, options MemoryReceiptStoreOptions) ReceiptStore
	Advance   func(d time.Duration)
	SkipClock bool
}

func runReceiptStoreContract(t *testing.T, contract receiptStoreContract) {
	t.Helper()
	t.Run(contract.Name+"/save_get", func(t *testing.T) {
		store := contract.NewStore(t, MemoryReceiptStoreOptions{TTL: time.Minute})
		receipt := testSignedReceipt(t, "req-1", 200)
		if err := store.Save(context.Background(), "req-1", receipt); err != nil {
			t.Fatal(err)
		}
		got, ok, err := store.Get(context.Background(), "req-1")
		if err != nil || !ok || got.RequestID != receipt.RequestID || got.Signature == nil || got.Signature.Value != receipt.Signature.Value {
			t.Fatalf("Get = %+v, ok=%t err=%v", got, ok, err)
		}
	})

	t.Run(contract.Name+"/rejects_invalid_without_publishing", func(t *testing.T) {
		store := contract.NewStore(t, MemoryReceiptStoreOptions{TTL: time.Minute})
		receipt := testSignedReceipt(t, "req-1", 200)
		if err := store.Save(context.Background(), "other", receipt); !errors.Is(err, ErrReceiptRequestIDMismatch) {
			t.Fatalf("request ID mismatch error = %v", err)
		}
		if got, ok, err := store.Get(context.Background(), "other"); err != nil || ok || got != (avr1.SignedReceiptV1{}) {
			t.Fatalf("mismatched receipt was published: %+v ok=%t err=%v", got, ok, err)
		}

		receipt.Signature = nil
		if err := store.Save(context.Background(), "req-1", receipt); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("unsigned receipt error = %v", err)
		}
		if got, ok, err := store.Get(context.Background(), "req-1"); err != nil || ok || got != (avr1.SignedReceiptV1{}) {
			t.Fatalf("unsigned receipt was published: %+v ok=%t err=%v", got, ok, err)
		}
	})

	t.Run(contract.Name+"/overwrite_refreshes_ttl", func(t *testing.T) {
		if contract.SkipClock {
			t.Skip("backend uses wall clock")
		}
		store := contract.NewStore(t, MemoryReceiptStoreOptions{TTL: time.Minute})
		if err := store.Save(context.Background(), "req-1", testSignedReceipt(t, "req-1", 200)); err != nil {
			t.Fatal(err)
		}
		contract.Advance(30 * time.Second)
		if err := store.Save(context.Background(), "req-1", testSignedReceipt(t, "req-1", 201)); err != nil {
			t.Fatal(err)
		}
		contract.Advance(45 * time.Second)
		got, ok, err := store.Get(context.Background(), "req-1")
		if err != nil || !ok || got.ResponseStatus != httpStatusCreated {
			t.Fatalf("Get overwritten = %+v ok=%t err=%v", got, ok, err)
		}
		contract.Advance(15 * time.Second)
		if got, ok, err := store.Get(context.Background(), "req-1"); err != nil || ok || got != (avr1.SignedReceiptV1{}) {
			t.Fatalf("expired = %+v ok=%t err=%v", got, ok, err)
		}
	})

	t.Run(contract.Name+"/get_miss", func(t *testing.T) {
		store := contract.NewStore(t, MemoryReceiptStoreOptions{TTL: time.Minute})
		if got, ok, err := store.Get(context.Background(), "missing"); err != nil || ok || got != (avr1.SignedReceiptV1{}) {
			t.Fatalf("Get missing = %+v ok=%t err=%v", got, ok, err)
		}
	})

	t.Run(contract.Name+"/copies_receipt", func(t *testing.T) {
		store := contract.NewStore(t, MemoryReceiptStoreOptions{TTL: time.Minute})
		receipt := testSignedReceipt(t, "req-1", 200)
		if err := store.Save(context.Background(), "req-1", receipt); err != nil {
			t.Fatal(err)
		}
		receipt.Signature.Value = "mutated-after-save"
		got, ok, err := store.Get(context.Background(), "req-1")
		if err != nil || !ok || got.Signature == nil || got.Signature.Value == "mutated-after-save" {
			t.Fatalf("stored receipt changed: %+v ok=%t err=%v", got, ok, err)
		}
		got.Signature.Value = "mutated-after-get"
		again, ok, err := store.Get(context.Background(), "req-1")
		if err != nil || !ok || again.Signature == nil || again.Signature.Value == "mutated-after-get" {
			t.Fatalf("reader mutation changed store: %+v ok=%t err=%v", again, ok, err)
		}
	})
}
