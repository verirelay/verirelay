package config

import (
	"reflect"
	"testing"
	"time"
)

var productionConfig = Config{
	Production:   true,
	ListenAddr:   ":8080",
	ReceiptStore: ReceiptStoreConfig{Type: StoreMemory, TTL: time.Hour, MaxEntries: 10000},
	RawStore:     RawStoreConfig{Type: StoreMemory, MaxBytes: 256 << 20},
	Provisioning: ProvisioningConfig{Timeout: 30 * time.Second},
}

// setDevelopmentVariables sets variables that development mode reads, each
// to a value that changes the configuration.
func setDevelopmentVariables(t *testing.T) {
	t.Helper()
	for key, value := range map[string]string{
		"VERIRELAY_LISTEN_ADDR":                "127.0.0.1:9999",
		"VERIRELAY_DEBUG_ERRORS":               "true",
		"VERIRELAY_ALLOW_PRIVATE_UPSTREAMS":    "true",
		"VERIRELAY_RECEIPT_STORE_TYPE":         "redis",
		"VERIRELAY_RECEIPT_STORE_ADDR":         "attacker.example:6379",
		"VERIRELAY_RUNTIME_CONFIG":             `{"version":"test"}`,
		"VERIRELAY_ALLOW_TEST_CONFIG":          "true",
		"VERIRELAY_FAKE_PROVISIONING_QUOTE":    "true",
		"VERIRELAY_FAKE_ATTESTATION":           "true",
		"VERIRELAY_VERIPROV_URL":               "https://attacker.example",
		"VERIRELAY_RECEIPT_KEY_SEED":           "test-seed",
		"VERIRELAY_DEV_ALLOW_RECEIPT_KEY_SEED": "true",
	} {
		t.Setenv(key, value)
	}
}

func TestLoadIsProductionUnlessDevelopmentIsExplicit(t *testing.T) {
	setDevelopmentVariables(t)
	for _, mode := range []string{"", "production", "Development", "dev", "development "} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("VERIRELAY_MODE", mode)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg, productionConfig) {
				t.Fatalf("config = %+v, want %+v", cfg, productionConfig)
			}
		})
	}
}
