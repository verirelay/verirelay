package storage

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"math"
	"testing"
	"time"

	"github.com/verirelay/protocol/avr1"
)

func TestMemoryAttestationStoreSaveGetAndRefreshTTL(t *testing.T) {
	now := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	store := NewMemoryAttestationStoreWithOptions(MemoryAttestationStoreOptions{Now: func() time.Time { return now }})
	attestation := testArchivedAttestation(t)

	if err := store.Save(context.Background(), attestation.KeyID, attestation, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.Get(context.Background(), attestation.KeyID)
	if err != nil || !ok || got != attestation {
		t.Fatalf("first get = (%+v, %t, %v)", got, ok, err)
	}

	now = now.Add(30 * time.Second)
	if err := store.Save(context.Background(), attestation.KeyID, attestation, time.Minute); err != nil {
		t.Fatal(err)
	}
	now = now.Add(45 * time.Second)
	if _, ok, err := store.Get(context.Background(), attestation.KeyID); err != nil || !ok {
		t.Fatalf("refreshed get = (%t, %v)", ok, err)
	}
	now = now.Add(20 * time.Second)
	if _, ok, err := store.Get(context.Background(), attestation.KeyID); err != nil || ok {
		t.Fatalf("expired get = (%t, %v)", ok, err)
	}
}

func TestArchivedAttestationTTLOutlivesReceiptsAndSaturates(t *testing.T) {
	if got := ArchivedAttestationTTL(time.Hour); got != 90*time.Minute {
		t.Fatalf("ArchivedAttestationTTL(1h) = %v", got)
	}
	if got := ArchivedAttestationTTL(math.MaxInt64 - time.Minute); got != math.MaxInt64 {
		t.Fatalf("ArchivedAttestationTTL near MaxInt64 = %v", got)
	}
}

func testArchivedAttestation(t *testing.T) avr1.AttestationResponse {
	t.Helper()
	privateKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return avr1.AttestationResponse{
		Version:                 avr1.AVRVersion,
		Nonce:                   base64.RawURLEncoding.EncodeToString(make([]byte, avr1.AVRNonceBytes)),
		ReceiptSigningPublicKey: base64.RawURLEncoding.EncodeToString(publicKey),
		KeyID:                   avr1.ReceiptKeyID(publicKey),
		Evidence:                avr1.AttestationEvidence{Format: avr1.AttestationEvidenceFormat, Report: "test"},
	}
}
