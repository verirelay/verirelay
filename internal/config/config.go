package config

import (
	"errors"
	"time"

	"github.com/verirelay/protocol/veriprov1"

	"github.com/verirelay/verirelay/internal/storage"
)

const (
	StoreMemory      = veriprov1.StoreTypeMemory
	StoreRedis       = veriprov1.StoreTypeRedis
	RawStoreDisabled = "disabled"

	productionListenAddr       = ":8080"
	defaultProvisioningTimeout = 30 * time.Second
	defaultRedisDialTimeout    = 5 * time.Second
	defaultRedisReadTimeout    = 2 * time.Second
	defaultRedisWriteTimeout   = 2 * time.Second
)

type Config struct {
	// Production is unset only in development mode.
	Production            bool
	ListenAddr            string
	DebugErrors           bool
	AllowPrivateUpstreams bool
	ReceiptKeySeed        string
	AdminToken            string
	FakeAttestation       bool
	ReceiptStore          ReceiptStoreConfig
	RawStore              RawStoreConfig
	Provisioning          ProvisioningConfig
	// Bootstrap is the exact Bootstrap file of a production Guest, disclosed
	// in the attestation response.
	Bootstrap []byte
}

// RedisConfig holds the connection settings of a Redis-backed store.
type RedisConfig struct {
	Addr         string
	TLS          bool
	Username     string
	Password     string
	DB           int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// ReceiptStoreConfig configures the Receipt store and the archived
// attestation store that shares it. Type is StoreMemory or StoreRedis;
// MaxEntries applies to memory, KeyPrefix and Redis to Redis.
type ReceiptStoreConfig struct {
	Type       string
	TTL        time.Duration
	MaxEntries int
	KeyPrefix  string
	Redis      RedisConfig
}

// RawStoreConfig configures the Raw Capture store. Type is StoreMemory,
// StoreRedis or, in development mode, RawStoreDisabled; MaxBytes applies to
// memory, KeyPrefix and Redis to Redis.
type RawStoreConfig struct {
	Type string
	// MaxTTL caps the retention a client may request; 0 means no cap.
	MaxTTL    time.Duration
	MaxBytes  int64
	KeyPrefix string
	Redis     RedisConfig
}

var (
	ErrInvalidReceiptStore = errors.New("invalid receipt store config")
	ErrInvalidRawStore     = errors.New("invalid raw store config")
)

func production() Config {
	return Config{
		Production:   true,
		ListenAddr:   productionListenAddr,
		ReceiptStore: defaultReceiptStore(),
		RawStore:     defaultRawStore(),
		Provisioning: ProvisioningConfig{Timeout: defaultProvisioningTimeout},
	}
}

func defaultReceiptStore() ReceiptStoreConfig {
	return ReceiptStoreConfig{Type: StoreMemory, TTL: storage.DefaultReceiptStoreTTL, MaxEntries: storage.DefaultReceiptStoreMaxEntries}
}

func defaultRawStore() RawStoreConfig {
	return RawStoreConfig{Type: StoreMemory, MaxBytes: storage.DefaultRawStoreMaxBytes}
}
