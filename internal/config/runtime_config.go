package config

import (
	"crypto/ed25519"
	"time"
)

// ProvisioningConfig says how the Guest obtains its Runtime Config. A
// production Guest provisions from the VeriProv its Bootstrap names. In
// development mode the Runtime Config comes from RuntimeConfig, from VeriProv
// at VeriProvURL with IdentityPublicKey, or not at all.
type ProvisioningConfig struct {
	// Timeout bounds one Provisioning attempt.
	Timeout           time.Duration
	VeriProvURL       string
	IdentityPublicKey ed25519.PublicKey
	RuntimeConfig     []byte
	FakeQuote         bool
}
