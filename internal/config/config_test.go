package config

import (
	"errors"
	"os"
	"reflect"
	"testing"
	"time"
)

func development(t *testing.T) {
	t.Helper()
	t.Setenv(ModeEnv, ModeDevelopment)
}

func TestLoadIsProductionUnlessDevelopmentIsExplicit(t *testing.T) {
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
	want := Config{
		Production:   true,
		ListenAddr:   ":8080",
		ReceiptStore: ReceiptStoreConfig{Type: StoreMemory, TTL: time.Hour, MaxEntries: 10000},
		RawStore:     RawStoreConfig{Type: StoreMemory, MaxBytes: 256 << 20},
		Provisioning: ProvisioningConfig{Timeout: 30 * time.Second},
	}
	for _, mode := range []string{"", "production", "Development", "dev", "development "} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv(ModeEnv, mode)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cfg, want) {
				t.Fatalf("config = %+v, want %+v", cfg, want)
			}
		})
	}
}

func TestLoadDevelopmentDefaults(t *testing.T) {
	development(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Production || cfg.ListenAddr != ":8080" || cfg.AllowPrivateUpstreams || cfg.FakeAttestation {
		t.Fatalf("unexpected development config: %+v", cfg)
	}
	if cfg.ReceiptStore != (ReceiptStoreConfig{Type: StoreMemory, TTL: time.Hour, MaxEntries: 10000}) {
		t.Fatalf("receipt store = %+v, want the Runtime Config defaults", cfg.ReceiptStore)
	}
	if cfg.RawStore != (RawStoreConfig{Type: StoreMemory, MaxBytes: 256 << 20}) {
		t.Fatalf("raw store = %+v, want the Runtime Config defaults", cfg.RawStore)
	}
}

func TestLoadRejectsReceiptKeySeedByDefault(t *testing.T) {
	development(t)
	t.Setenv("VERIRELAY_RECEIPT_KEY_SEED", "seed")
	t.Setenv("VERIRELAY_DEV_ALLOW_RECEIPT_KEY_SEED", "false")
	if _, err := Load(); !errors.Is(err, ErrReceiptKeySeedDisabled) {
		t.Fatalf("seed guard err = %v", err)
	}
}

func TestLoadAllowsReceiptKeySeedWithDevOverride(t *testing.T) {
	development(t)
	t.Setenv("VERIRELAY_RECEIPT_KEY_SEED", "seed")
	t.Setenv("VERIRELAY_DEV_ALLOW_RECEIPT_KEY_SEED", "true")
	t.Setenv("VERIRELAY_FAKE_ATTESTATION", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReceiptKeySeed != "seed" || !cfg.FakeAttestation {
		t.Fatalf("development config was not loaded: %+v", cfg)
	}
}

func TestLoadRedisReceiptStoreConfig(t *testing.T) {
	development(t)
	passwordFile := writeTempPassword(t, "secret\n")
	t.Setenv("VERIRELAY_RECEIPT_STORE_TYPE", "valkey")
	t.Setenv("VERIRELAY_RECEIPT_STORE_ADDR", "127.0.0.1:6379")
	t.Setenv("VERIRELAY_RECEIPT_STORE_TLS", "true")
	t.Setenv("VERIRELAY_RECEIPT_STORE_USERNAME", "default")
	t.Setenv("VERIRELAY_RECEIPT_STORE_PASSWORD_FILE", passwordFile)
	t.Setenv("VERIRELAY_RECEIPT_STORE_DB", "2")
	t.Setenv("VERIRELAY_RECEIPT_STORE_TTL", "2h")
	t.Setenv("VERIRELAY_RECEIPT_STORE_CONNECT_TIMEOUT", "3s")
	t.Setenv("VERIRELAY_RECEIPT_STORE_READ_TIMEOUT", "4s")
	t.Setenv("VERIRELAY_RECEIPT_STORE_WRITE_TIMEOUT", "5s")
	t.Setenv("VERIRELAY_RECEIPT_STORE_KEY_PREFIX", "test:")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := ReceiptStoreConfig{
		Type: StoreRedis, TTL: 2 * time.Hour, MaxEntries: 10000, KeyPrefix: "test:",
		Redis: RedisConfig{Addr: "127.0.0.1:6379", TLS: true, Username: "default", Password: "secret", DB: 2, DialTimeout: 3 * time.Second, ReadTimeout: 4 * time.Second, WriteTimeout: 5 * time.Second},
	}
	if cfg.ReceiptStore != want {
		t.Fatalf("receipt store = %+v, want %+v", cfg.ReceiptStore, want)
	}
	if cfg.RawStore != (RawStoreConfig{Type: StoreRedis, MaxBytes: 256 << 20, KeyPrefix: "test:", Redis: want.Redis}) {
		t.Fatalf("raw store = %+v, want the receipt store's Redis and key prefix", cfg.RawStore)
	}
}

func TestLoadRedisDefaultKeyPrefix(t *testing.T) {
	development(t)
	t.Setenv("VERIRELAY_RECEIPT_STORE_TYPE", "redis")
	t.Setenv("VERIRELAY_RECEIPT_STORE_ADDR", "127.0.0.1:6379")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReceiptStore.KeyPrefix != "verirelay:" {
		t.Fatalf("key prefix = %q", cfg.ReceiptStore.KeyPrefix)
	}
}

func TestLoadRawStoreConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want RawStoreConfig
	}{
		{name: "disabled", env: map[string]string{"VERIRELAY_RAW_STORE_TYPE": "disabled"}, want: RawStoreConfig{Type: RawStoreDisabled, MaxBytes: 256 << 20}},
		{name: "memory", env: map[string]string{"VERIRELAY_RAW_STORE_TYPE": "memory", "VERIRELAY_RAW_STORE_MAX_BYTES": "1024", "VERIRELAY_RAW_STORE_MAX_TTL": "24h"}, want: RawStoreConfig{Type: StoreMemory, MaxBytes: 1024, MaxTTL: 24 * time.Hour}},
		{name: "redis", env: map[string]string{"VERIRELAY_RAW_STORE_TYPE": "redis", "VERIRELAY_RAW_STORE_ADDR": "redis:6379", "VERIRELAY_RAW_STORE_KEY_PREFIX": "raw-tenant:"}, want: RawStoreConfig{
			Type: StoreRedis, MaxBytes: 256 << 20, KeyPrefix: "raw-tenant:",
			Redis: RedisConfig{Addr: "redis:6379", DialTimeout: 5 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			development(t)
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.RawStore != tc.want {
				t.Fatalf("raw store = %+v, want %+v", cfg.RawStore, tc.want)
			}
		})
	}
}

func TestLoadRejectsInvalidRawStoreConfig(t *testing.T) {
	for key, value := range map[string]string{
		"VERIRELAY_RAW_STORE_TYPE":      "postgres",
		"VERIRELAY_RAW_STORE_MAX_TTL":   "0s",
		"VERIRELAY_RAW_STORE_MAX_BYTES": "0",
	} {
		t.Run(key, func(t *testing.T) {
			development(t)
			t.Setenv(key, value)
			if _, err := Load(); !errors.Is(err, ErrInvalidRawStore) {
				t.Fatalf("%s=%s: Load err = %v", key, value, err)
			}
		})
	}
	t.Run("redis without addr", func(t *testing.T) {
		development(t)
		t.Setenv("VERIRELAY_RAW_STORE_TYPE", "redis")
		if _, err := Load(); !errors.Is(err, ErrInvalidRawStore) {
			t.Fatalf("Load err = %v", err)
		}
	})
}

func TestLoadRejectsInvalidReceiptStoreConfig(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"type", "VERIRELAY_RECEIPT_STORE_TYPE", "postgres"},
		{"ttl", "VERIRELAY_RECEIPT_STORE_TTL", "0s"},
		{"max entries", "VERIRELAY_RECEIPT_STORE_MAX_ENTRIES", "0"},
		{"missing addr", "VERIRELAY_RECEIPT_STORE_ADDR", ""},
		{"tls", "VERIRELAY_RECEIPT_STORE_TLS", "maybe"},
		{"db", "VERIRELAY_RECEIPT_STORE_DB", "-1"},
		{"timeout", "VERIRELAY_RECEIPT_STORE_READ_TIMEOUT", "0s"},
		{"prefix", "VERIRELAY_RECEIPT_STORE_KEY_PREFIX", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			development(t)
			if tc.key != "VERIRELAY_RECEIPT_STORE_TYPE" {
				t.Setenv("VERIRELAY_RECEIPT_STORE_TYPE", "redis")
				t.Setenv("VERIRELAY_RECEIPT_STORE_ADDR", "127.0.0.1:6379")
			}
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); !errors.Is(err, ErrInvalidReceiptStore) {
				t.Fatalf("Load err = %v", err)
			}
		})
	}
}

func TestLoadRejectsBadPasswordFile(t *testing.T) {
	development(t)
	t.Setenv("VERIRELAY_RECEIPT_STORE_TYPE", "redis")
	t.Setenv("VERIRELAY_RECEIPT_STORE_ADDR", "127.0.0.1:6379")
	t.Setenv("VERIRELAY_RECEIPT_STORE_PASSWORD_FILE", "/missing/nope")
	if _, err := Load(); !errors.Is(err, ErrInvalidReceiptStore) {
		t.Fatalf("missing password file err = %v", err)
	}
	t.Setenv("VERIRELAY_RECEIPT_STORE_PASSWORD_FILE", writeTempPassword(t, ""))
	if _, err := Load(); !errors.Is(err, ErrInvalidReceiptStore) {
		t.Fatalf("empty password file err = %v", err)
	}
}

func writeTempPassword(t *testing.T, content string) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "password-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return file.Name()
}
