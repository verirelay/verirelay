package config

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/verirelay/protocol/veriprov1"
)

const maxRuntimeConfigBytes int64 = 1 << 20

var ErrInvalidProvisioning = errors.New("invalid provisioning config")

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

func developmentProvisioningConfig() (ProvisioningConfig, error) {
	cfg := ProvisioningConfig{Timeout: defaultProvisioningTimeout}
	allowTestConfig, err := strictOptionalBool("VERIRELAY_ALLOW_TEST_CONFIG", false)
	if err != nil {
		return cfg, err
	}
	if cfg.FakeQuote, err = strictOptionalBool("VERIRELAY_FAKE_PROVISIONING_QUOTE", false); err != nil {
		return cfg, err
	}
	if err := parseOptionalDuration("VERIRELAY_PROVISIONING_TIMEOUT", &cfg.Timeout, ErrInvalidProvisioning); err != nil {
		return cfg, err
	}
	veriProvURL := strings.TrimSpace(os.Getenv("VERIRELAY_VERIPROV_URL"))
	enabled, err := strictOptionalBool("VERIRELAY_PROVISIONING_ENABLED", veriProvURL != "")
	if err != nil {
		return cfg, err
	}
	if cfg.RuntimeConfig, err = runtimeConfigSource(); err != nil {
		return cfg, fmt.Errorf("%w: %w", ErrInvalidProvisioning, err)
	}
	if len(cfg.RuntimeConfig) > 0 && !allowTestConfig {
		return cfg, fmt.Errorf("%w: local runtime config requires VERIRELAY_ALLOW_TEST_CONFIG=true", ErrInvalidProvisioning)
	}
	if cfg.FakeQuote && !allowTestConfig {
		return cfg, fmt.Errorf("%w: fake provisioning quote requires VERIRELAY_ALLOW_TEST_CONFIG=true", ErrInvalidProvisioning)
	}
	if !enabled {
		return cfg, nil
	}
	if veriProvURL == "" {
		return cfg, fmt.Errorf("%w: VERIRELAY_VERIPROV_URL is required", ErrInvalidProvisioning)
	}
	parsed, err := url.Parse(veriProvURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return cfg, fmt.Errorf("%w: VERIRELAY_VERIPROV_URL must be an HTTPS origin", ErrInvalidProvisioning)
	}
	identity, err := veriprov1.DecodeBase64URL(os.Getenv("VERIRELAY_VERIPROV_IDENTITY_PUBLIC_KEY"), "VERIRELAY_VERIPROV_IDENTITY_PUBLIC_KEY", ed25519.PublicKeySize)
	if err != nil {
		return cfg, fmt.Errorf("%w: %w", ErrInvalidProvisioning, err)
	}
	cfg.VeriProvURL, cfg.IdentityPublicKey = veriProvURL, identity
	return cfg, nil
}

func strictOptionalBool(key string, fallback bool) (bool, error) {
	value := fallback
	if err := parseOptionalStrictBool(key, &value, ErrInvalidProvisioning); err != nil {
		return false, err
	}
	return value, nil
}

func runtimeConfigSource() ([]byte, error) {
	direct, directSet := os.LookupEnv("VERIRELAY_RUNTIME_CONFIG")
	path, fileSet := os.LookupEnv("VERIRELAY_RUNTIME_CONFIG_FILE")
	if directSet && fileSet {
		return nil, errors.New("VERIRELAY_RUNTIME_CONFIG and VERIRELAY_RUNTIME_CONFIG_FILE are mutually exclusive")
	}
	if directSet {
		if strings.TrimSpace(direct) == "" {
			return nil, errors.New("VERIRELAY_RUNTIME_CONFIG must not be blank")
		}
		if int64(len(direct)) > maxRuntimeConfigBytes {
			return nil, errors.New("VERIRELAY_RUNTIME_CONFIG exceeds size limit")
		}
		return []byte(direct), nil
	}
	if !fileSet {
		return nil, nil
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("VERIRELAY_RUNTIME_CONFIG_FILE must be a regular readable file")
	}
	if info.Size() > maxRuntimeConfigBytes {
		return nil, errors.New("VERIRELAY_RUNTIME_CONFIG_FILE exceeds size limit")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("VERIRELAY_RUNTIME_CONFIG_FILE cannot be read")
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, errors.New("VERIRELAY_RUNTIME_CONFIG_FILE must not be blank")
	}
	return data, nil
}
