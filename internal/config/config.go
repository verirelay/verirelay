package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/verirelay/protocol/veriprov1"

	"github.com/verirelay/verirelay/internal/storage"
)

const (
	StoreMemory      = veriprov1.StoreTypeMemory
	StoreRedis       = veriprov1.StoreTypeRedis
	RawStoreDisabled = "disabled"

	// ModeEnv selects development mode when it is exactly ModeDevelopment.
	ModeEnv         = "VERIRELAY_MODE"
	ModeDevelopment = "development"

	productionListenAddr       = ":8080"
	defaultProvisioningTimeout = 30 * time.Second
	defaultRedisDialTimeout    = 5 * time.Second
	defaultRedisReadTimeout    = 2 * time.Second
	defaultRedisWriteTimeout   = 2 * time.Second
)

type Config struct {
	// Production is set unless VERIRELAY_MODE=development.
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
	ErrReceiptKeySeedDisabled = errors.New("deterministic receipt key seeds are disabled unless VERIRELAY_DEV_ALLOW_RECEIPT_KEY_SEED=true")
	ErrInvalidReceiptStore    = errors.New("invalid receipt store config")
	ErrInvalidRawStore        = errors.New("invalid raw store config")
)

// Load returns the development configuration from the environment when
// VERIRELAY_MODE=development, and otherwise the production configuration,
// which no environment variable changes.
func Load() (Config, error) {
	if os.Getenv(ModeEnv) == ModeDevelopment {
		return loadDevelopment()
	}
	return Config{
		Production:   true,
		ListenAddr:   productionListenAddr,
		ReceiptStore: defaultReceiptStore(),
		RawStore:     defaultRawStore(),
		Provisioning: ProvisioningConfig{Timeout: defaultProvisioningTimeout},
	}, nil
}

func defaultReceiptStore() ReceiptStoreConfig {
	return ReceiptStoreConfig{Type: StoreMemory, TTL: storage.DefaultReceiptStoreTTL, MaxEntries: storage.DefaultReceiptStoreMaxEntries}
}

func defaultRawStore() RawStoreConfig {
	return RawStoreConfig{Type: StoreMemory, MaxBytes: storage.DefaultRawStoreMaxBytes}
}

func loadDevelopment() (Config, error) {
	loadDotEnv(".env")
	receiptKeySeed := getenv("VERIRELAY_RECEIPT_KEY_SEED", "")
	if receiptKeySeed != "" && !boolEnv("VERIRELAY_DEV_ALLOW_RECEIPT_KEY_SEED") {
		return Config{}, ErrReceiptKeySeedDisabled
	}
	receipts, err := receiptStoreConfig()
	if err != nil {
		return Config{}, err
	}
	raw, err := rawStoreConfig(receipts)
	if err != nil {
		return Config{}, err
	}
	provisioning, err := developmentProvisioningConfig()
	if err != nil {
		return Config{}, err
	}
	return Config{
		ListenAddr:            getenv("VERIRELAY_LISTEN_ADDR", productionListenAddr),
		DebugErrors:           boolEnv("VERIRELAY_DEBUG_ERRORS"),
		AllowPrivateUpstreams: boolEnv("VERIRELAY_ALLOW_PRIVATE_UPSTREAMS"),
		ReceiptKeySeed:        receiptKeySeed,
		FakeAttestation:       boolEnv("VERIRELAY_FAKE_ATTESTATION"),
		ReceiptStore:          receipts,
		RawStore:              raw,
		Provisioning:          provisioning,
	}, nil
}

func receiptStoreConfig() (ReceiptStoreConfig, error) {
	cfg := defaultReceiptStore()
	storeType, err := storeTypeEnv("VERIRELAY_RECEIPT_STORE_TYPE", StoreMemory, ErrInvalidReceiptStore, StoreMemory, StoreRedis)
	if err != nil {
		return cfg, err
	}
	cfg.Type = storeType
	if err := parseOptionalDuration("VERIRELAY_RECEIPT_STORE_TTL", &cfg.TTL, ErrInvalidReceiptStore); err != nil {
		return cfg, err
	}
	if err := parseOptionalPositiveInt("VERIRELAY_RECEIPT_STORE_MAX_ENTRIES", &cfg.MaxEntries, ErrInvalidReceiptStore); err != nil {
		return cfg, err
	}
	if cfg.Type != StoreRedis {
		return cfg, nil
	}
	cfg.KeyPrefix, cfg.Redis, err = redisStoreEnv("VERIRELAY_RECEIPT_STORE_", ErrInvalidReceiptStore)
	return cfg, err
}

// rawStoreConfig follows the Runtime Config rule when VERIRELAY_RAW_STORE_TYPE
// is unset: a Redis Receipt store shares its connection and key prefix, and
// otherwise the Raw Capture store is a memory store.
func rawStoreConfig(receipts ReceiptStoreConfig) (RawStoreConfig, error) {
	cfg := defaultRawStore()
	storeType, err := storeTypeEnv("VERIRELAY_RAW_STORE_TYPE", "", ErrInvalidRawStore, StoreMemory, StoreRedis, RawStoreDisabled)
	if err != nil {
		return cfg, err
	}
	if err := parseOptionalDuration("VERIRELAY_RAW_STORE_MAX_TTL", &cfg.MaxTTL, ErrInvalidRawStore); err != nil {
		return cfg, err
	}
	if err := parseOptionalPositiveInt64("VERIRELAY_RAW_STORE_MAX_BYTES", &cfg.MaxBytes, ErrInvalidRawStore); err != nil {
		return cfg, err
	}
	switch {
	case storeType == "" && receipts.Type == StoreRedis:
		cfg.Type, cfg.KeyPrefix, cfg.Redis = StoreRedis, receipts.KeyPrefix, receipts.Redis
	case storeType == StoreRedis:
		cfg.Type = StoreRedis
		cfg.KeyPrefix, cfg.Redis, err = redisStoreEnv("VERIRELAY_RAW_STORE_", ErrInvalidRawStore)
	case storeType != "":
		cfg.Type = storeType
	}
	return cfg, err
}

// storeTypeEnv reads a store type, accepting valkey as redis.
func storeTypeEnv(key, fallback string, invalid error, allowed ...string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback, nil
	}
	if value == "valkey" {
		value = StoreRedis
	}
	for _, storeType := range allowed {
		if value == storeType {
			return value, nil
		}
	}
	return "", fmt.Errorf("%w: unsupported type %q", invalid, value)
}

func redisStoreEnv(envPrefix string, invalid error) (string, RedisConfig, error) {
	cfg := RedisConfig{
		Addr:         getenv(envPrefix+"ADDR", ""),
		Username:     getenv(envPrefix+"USERNAME", ""),
		DialTimeout:  defaultRedisDialTimeout,
		ReadTimeout:  defaultRedisReadTimeout,
		WriteTimeout: defaultRedisWriteTimeout,
	}
	if cfg.Addr == "" {
		return "", cfg, fmt.Errorf("%w: %sADDR is required", invalid, envPrefix)
	}
	keyPrefix := veriprov1.DefaultKeyPrefix
	if value, ok := os.LookupEnv(envPrefix + "KEY_PREFIX"); ok {
		keyPrefix = value
	}
	if keyPrefix == "" {
		return "", cfg, fmt.Errorf("%w: %sKEY_PREFIX must not be empty", invalid, envPrefix)
	}
	if err := parseOptionalStrictBool(envPrefix+"TLS", &cfg.TLS, invalid); err != nil {
		return "", cfg, err
	}
	if err := parseOptionalNonNegativeInt(envPrefix+"DB", &cfg.DB, invalid); err != nil {
		return "", cfg, err
	}
	for key, target := range map[string]*time.Duration{"CONNECT_TIMEOUT": &cfg.DialTimeout, "READ_TIMEOUT": &cfg.ReadTimeout, "WRITE_TIMEOUT": &cfg.WriteTimeout} {
		if err := parseOptionalDuration(envPrefix+key, target, invalid); err != nil {
			return "", cfg, err
		}
	}
	if path := os.Getenv(envPrefix + "PASSWORD_FILE"); path != "" {
		password, err := readPasswordFile(path, invalid)
		if err != nil {
			return "", cfg, err
		}
		cfg.Password = password
	}
	return keyPrefix, cfg, nil
}

func parseOptionalDuration(key string, target *time.Duration, invalid error) error {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return fmt.Errorf("%w: %s must be a positive duration", invalid, key)
	}
	*target = d
	return nil
}

func parseOptionalStrictBool(key string, target *bool, invalid error) error {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}
	b, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("%w: %s must be a bool", invalid, key)
	}
	*target = b
	return nil
}

func parseOptionalNonNegativeInt(key string, target *int, invalid error) error {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return fmt.Errorf("%w: %s must be a non-negative integer", invalid, key)
	}
	*target = n
	return nil
}

func parseOptionalPositiveInt(key string, target *int, invalid error) error {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fmt.Errorf("%w: %s must be a positive integer", invalid, key)
	}
	*target = n
	return nil
}

func parseOptionalPositiveInt64(key string, target *int64, invalid error) error {
	value := os.Getenv(key)
	if value == "" {
		return nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return fmt.Errorf("%w: %s must be a positive integer", invalid, key)
	}
	*target = n
	return nil
}

func readPasswordFile(path string, invalid error) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G703: the path comes from development configuration, never from requests
	if err != nil {
		return "", fmt.Errorf("%w: password file cannot be read", invalid)
	}
	password := strings.TrimRight(string(data), "\r\n")
	if password == "" {
		return "", fmt.Errorf("%w: password file is empty", invalid)
	}
	return password, nil
}

func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		_ = os.Setenv(key, value)
	}
}

func getenv(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func boolEnv(key string) bool {
	b, _ := strconv.ParseBool(os.Getenv(key))
	return b
}
