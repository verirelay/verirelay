package config

import (
	"crypto/ed25519"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/verirelay/protocol/veriprov1"
)

func runtimeConfig(stores func(*veriprov1.RuntimeConfigV1)) veriprov1.RuntimeConfigV1 {
	runtime := veriprov1.RuntimeConfigV1{Version: veriprov1.RuntimeConfigVersion, AdminToken: "test-only-admin-token"}
	if stores != nil {
		stores(&runtime)
	}
	return runtime
}

func ptr[T any](value T) *T { return &value }

var defaultTestRedis = RedisConfig{Addr: "redis.internal.example:6379", DialTimeout: 5 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second}

func TestApplyRuntimeConfigRejectsInvalidConfig(t *testing.T) {
	for _, runtime := range []veriprov1.RuntimeConfigV1{
		{},
		{Version: veriprov1.RuntimeConfigVersion},
		{Version: "verirelay-runtime-config/2", AdminToken: "token"},
	} {
		if _, err := ApplyRuntimeConfig(Config{}, runtime); !errors.Is(err, ErrInvalidRuntimeConfig) {
			t.Fatalf("ApplyRuntimeConfig(%+v) err = %v", runtime, err)
		}
	}
}

func TestApplyRuntimeConfigDefaultsBothStoresToMemory(t *testing.T) {
	cfg := Config{ListenAddr: ":8080", ReceiptStore: ReceiptStoreConfig{Type: StoreRedis}, RawStore: RawStoreConfig{Type: RawStoreDisabled}}
	got, err := ApplyRuntimeConfig(cfg, runtimeConfig(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got.ListenAddr != ":8080" || got.AdminToken != "test-only-admin-token" {
		t.Fatalf("config = %+v", got)
	}
	if got.ReceiptStore != defaultReceiptStore() || got.RawStore != defaultRawStore() {
		t.Fatalf("stores = %+v / %+v, want the memory defaults", got.ReceiptStore, got.RawStore)
	}
}

func TestApplyRuntimeConfigMemoryStores(t *testing.T) {
	got, err := ApplyRuntimeConfig(Config{}, runtimeConfig(func(runtime *veriprov1.RuntimeConfigV1) {
		runtime.ReceiptStore = &veriprov1.RuntimeReceiptStoreConfigV1{Type: "memory", TTLSeconds: ptr[int64](60), MaxEntries: ptr(5)}
		runtime.RawStore = &veriprov1.RuntimeRawStoreConfigV1{Type: "memory", MaxTTLSeconds: 86400, MaxBytes: ptr[int64](1024)}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.ReceiptStore != (ReceiptStoreConfig{Type: StoreMemory, TTL: time.Minute, MaxEntries: 5}) {
		t.Fatalf("receipt store = %+v", got.ReceiptStore)
	}
	if got.RawStore != (RawStoreConfig{Type: StoreMemory, MaxTTL: 24 * time.Hour, MaxBytes: 1024}) {
		t.Fatalf("raw store = %+v", got.RawStore)
	}
}

func TestApplyRuntimeConfigRedisReceiptStoreSharesWithRawStore(t *testing.T) {
	got, err := ApplyRuntimeConfig(Config{}, runtimeConfig(func(runtime *veriprov1.RuntimeConfigV1) {
		runtime.ReceiptStore = &veriprov1.RuntimeReceiptStoreConfigV1{Type: "redis", Addr: "redis.internal.example:6379", TLS: true, Username: "verirelay", Password: "test-only-redis-password", DB: 2}
	}))
	if err != nil {
		t.Fatal(err)
	}
	redis := RedisConfig{Addr: "redis.internal.example:6379", TLS: true, Username: "verirelay", Password: "test-only-redis-password", DB: 2, DialTimeout: 5 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second}
	if got.ReceiptStore != (ReceiptStoreConfig{Type: StoreRedis, TTL: time.Hour, MaxEntries: 10000, KeyPrefix: "verirelay:", Redis: redis}) {
		t.Fatalf("receipt store = %+v", got.ReceiptStore)
	}
	if got.RawStore != (RawStoreConfig{Type: StoreRedis, MaxBytes: 256 << 20, KeyPrefix: "verirelay:", Redis: redis}) {
		t.Fatalf("raw store = %+v, want the receipt store's Redis and key prefix", got.RawStore)
	}
}

func TestApplyRuntimeConfigRedisRawStore(t *testing.T) {
	got, err := ApplyRuntimeConfig(Config{}, runtimeConfig(func(runtime *veriprov1.RuntimeConfigV1) {
		runtime.RawStore = &veriprov1.RuntimeRawStoreConfigV1{Type: "redis", Addr: "redis.internal.example:6379", KeyPrefix: ptr("raw:"), MaxTTLSeconds: 60}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.ReceiptStore != defaultReceiptStore() {
		t.Fatalf("receipt store = %+v", got.ReceiptStore)
	}
	if got.RawStore != (RawStoreConfig{Type: StoreRedis, MaxTTL: time.Minute, MaxBytes: 256 << 20, KeyPrefix: "raw:", Redis: defaultTestRedis}) {
		t.Fatalf("raw store = %+v", got.RawStore)
	}
}

func TestApplyRuntimeConfigSaturatesTTL(t *testing.T) {
	got, err := ApplyRuntimeConfig(Config{}, runtimeConfig(func(runtime *veriprov1.RuntimeConfigV1) {
		runtime.ReceiptStore = &veriprov1.RuntimeReceiptStoreConfigV1{Type: "memory", TTLSeconds: ptr[int64](math.MaxInt64)}
		runtime.RawStore = &veriprov1.RuntimeRawStoreConfigV1{Type: "memory", MaxTTLSeconds: math.MaxInt64}
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got.ReceiptStore.TTL != math.MaxInt64 || got.RawStore.MaxTTL != math.MaxInt64 {
		t.Fatalf("TTLs = %v / %v, want saturation", got.ReceiptStore.TTL, got.RawStore.MaxTTL)
	}
}

func TestDevelopmentProvisioningConfig(t *testing.T) {
	identity := make([]byte, ed25519.PublicKeySize)
	identity[0] = 1
	t.Run("veriprov", func(t *testing.T) {
		t.Setenv("VERIRELAY_VERIPROV_URL", "https://veriprov.example:8443")
		t.Setenv("VERIRELAY_VERIPROV_IDENTITY_PUBLIC_KEY", veriprov1.EncodeBase64URL(identity))
		cfg, err := developmentProvisioningConfig()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.VeriProvURL != "https://veriprov.example:8443" || !cfg.IdentityPublicKey.Equal(ed25519.PublicKey(identity)) || cfg.Timeout != 30*time.Second {
			t.Fatalf("provisioning = %+v", cfg)
		}
	})
	t.Run("disabled", func(t *testing.T) {
		t.Setenv("VERIRELAY_VERIPROV_URL", "https://veriprov.example")
		t.Setenv("VERIRELAY_PROVISIONING_ENABLED", "false")
		cfg, err := developmentProvisioningConfig()
		if err != nil || cfg.VeriProvURL != "" {
			t.Fatalf("provisioning = %+v, %v", cfg, err)
		}
	})
}

func TestDevelopmentProvisioningConfigErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "invalid bool", env: map[string]string{"VERIRELAY_ALLOW_TEST_CONFIG": "maybe"}, want: "invalid provisioning config: VERIRELAY_ALLOW_TEST_CONFIG must be a bool"},
		{name: "blank runtime config", env: map[string]string{"VERIRELAY_ALLOW_TEST_CONFIG": "true", "VERIRELAY_RUNTIME_CONFIG": "   "}, want: "invalid provisioning config: VERIRELAY_RUNTIME_CONFIG must not be blank"},
		{name: "runtime config without test config", env: map[string]string{"VERIRELAY_RUNTIME_CONFIG": "{}"}, want: "invalid provisioning config: local runtime config requires VERIRELAY_ALLOW_TEST_CONFIG=true"},
		{name: "enabled without url", env: map[string]string{"VERIRELAY_PROVISIONING_ENABLED": "true"}, want: "invalid provisioning config: VERIRELAY_VERIPROV_URL is required"},
		{name: "http url", env: map[string]string{"VERIRELAY_VERIPROV_URL": "http://veriprov.example"}, want: "invalid provisioning config: VERIRELAY_VERIPROV_URL must be an HTTPS origin"},
		{name: "missing identity key", env: map[string]string{"VERIRELAY_VERIPROV_URL": "https://veriprov.example"}, want: "invalid provisioning config: VERIRELAY_VERIPROV_IDENTITY_PUBLIC_KEY must not be empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			_, err := developmentProvisioningConfig()
			if !errors.Is(err, ErrInvalidProvisioning) || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
