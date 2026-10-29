package keys

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"

	"github.com/verirelay/protocol/avr1"
)

func TestNewReceiptSignerUsesSeed(t *testing.T) {
	seed := []byte("0123456789abcdef0123456789abcdef")
	signer, err := NewReceiptSigner(base64.RawURLEncoding.EncodeToString(seed))
	if err != nil {
		t.Fatal(err)
	}
	want := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	if signer.PublicKeyBase64URL() != base64.RawURLEncoding.EncodeToString(want) || signer.KeyID() != avr1.ReceiptKeyID(want) {
		t.Fatalf("signer key = %s (%s), want seeded key", signer.PublicKeyBase64URL(), signer.KeyID())
	}
}

func TestNewReceiptSignerGeneratesKeyWithoutSeed(t *testing.T) {
	first, err := NewReceiptSigner("")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewReceiptSigner("")
	if err != nil {
		t.Fatal(err)
	}
	if first.KeyID() == second.KeyID() {
		t.Fatal("generated receipt signing keys are identical")
	}
}

func TestNewReceiptSignerRejectsInvalidSeed(t *testing.T) {
	for _, seed := range []string{"not base64url!", base64.RawURLEncoding.EncodeToString([]byte("short"))} {
		if _, err := NewReceiptSigner(seed); err == nil {
			t.Errorf("NewReceiptSigner(%q) succeeded", seed)
		}
	}
}
