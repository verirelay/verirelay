package config

import (
	"errors"
	"fmt"

	"github.com/verirelay/protocol/veriprov1"
)

var ErrInvalidRuntimeConfig = errors.New("invalid runtime config")

// ApplyRuntimeConfig validates runtime and returns cfg with its admin token
// and stores. Absent fields take the Runtime Config defaults.
func ApplyRuntimeConfig(cfg Config, runtime veriprov1.RuntimeConfigV1) (Config, error) {
	if err := runtime.Validate(); err != nil {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalidRuntimeConfig, err)
	}
	cfg.AdminToken = runtime.AdminToken
	cfg.ReceiptStore = runtimeReceiptStore(runtime.ReceiptStore)
	cfg.RawStore = runtimeRawStore(runtime.RawStore, cfg.ReceiptStore)
	return cfg, nil
}

func runtimeReceiptStore(store *veriprov1.RuntimeReceiptStoreConfigV1) ReceiptStoreConfig {
	cfg := defaultReceiptStore()
	if store == nil {
		return cfg
	}
	cfg.Type = store.Type
	if store.TTLSeconds != nil {
		cfg.TTL = veriprov1.DurationFromSeconds(*store.TTLSeconds)
	}
	switch store.Type {
	case StoreMemory:
		if store.MaxEntries != nil {
			cfg.MaxEntries = *store.MaxEntries
		}
	case StoreRedis:
		cfg.KeyPrefix = keyPrefix(store.KeyPrefix)
		cfg.Redis = runtimeRedis(store.Addr, store.TLS, store.Username, store.Password, store.DB)
	}
	return cfg
}

// runtimeRawStore applies raw_store. Without it, a Redis Receipt store shares
// its connection and key prefix with the Raw Capture store, and otherwise the
// Raw Capture store is a memory store with the defaults.
func runtimeRawStore(store *veriprov1.RuntimeRawStoreConfigV1, receipts ReceiptStoreConfig) RawStoreConfig {
	cfg := defaultRawStore()
	if store == nil {
		if receipts.Type == StoreRedis {
			cfg.Type, cfg.KeyPrefix, cfg.Redis = StoreRedis, receipts.KeyPrefix, receipts.Redis
		}
		return cfg
	}
	cfg.Type = store.Type
	cfg.MaxTTL = veriprov1.DurationFromSeconds(store.MaxTTLSeconds)
	switch store.Type {
	case StoreMemory:
		if store.MaxBytes != nil {
			cfg.MaxBytes = *store.MaxBytes
		}
	case StoreRedis:
		cfg.KeyPrefix = keyPrefix(store.KeyPrefix)
		cfg.Redis = runtimeRedis(store.Addr, store.TLS, store.Username, store.Password, store.DB)
	}
	return cfg
}

func keyPrefix(value *string) string {
	if value == nil {
		return veriprov1.DefaultKeyPrefix
	}
	return *value
}

func runtimeRedis(addr string, tls bool, username, password string, db int) RedisConfig {
	return RedisConfig{
		Addr:         addr,
		TLS:          tls,
		Username:     username,
		Password:     password,
		DB:           db,
		DialTimeout:  defaultRedisDialTimeout,
		ReadTimeout:  defaultRedisReadTimeout,
		WriteTimeout: defaultRedisWriteTimeout,
	}
}
