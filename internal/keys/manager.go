package keys

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"

	"github.com/verirelay/protocol/avr1"
)

// NewReceiptSigner returns the Receipt signer for an unpadded base64url
// Ed25519 seed, or for a freshly generated key when the seed is empty.
func NewReceiptSigner(seedBase64URL string) (*avr1.ReceiptSignerV1, error) {
	private, err := loadPrivateKey(seedBase64URL)
	if err != nil {
		return nil, err
	}
	return avr1.NewReceiptSignerV1(private)
}

func loadPrivateKey(seedBase64URL string) (ed25519.PrivateKey, error) {
	if seedBase64URL == "" {
		_, generated, err := ed25519.GenerateKey(nil)
		return generated, err
	}
	seed, err := base64.RawURLEncoding.DecodeString(seedBase64URL)
	if err != nil {
		return nil, fmt.Errorf("decode receipt key seed: %w", err)
	}
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("invalid receipt key seed length: %d", len(seed))
	}
	return ed25519.NewKeyFromSeed(seed), nil
}
