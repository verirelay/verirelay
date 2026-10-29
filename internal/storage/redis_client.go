package storage

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// Redis key namespaces under the configured key prefix.
const (
	receiptKeyNamespace     = "receipt:"
	attestationKeyNamespace = "attestation:"
	rawKeyNamespace         = "raw:"
)

// RedisOptions are the connection settings of a Redis server.
type RedisOptions struct {
	Addr         string
	TLS          bool
	Username     string
	Password     string
	DB           int
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// NewRedisClient returns a client for options. It connects lazily, so stores
// that share one Redis server share one client.
func NewRedisClient(options RedisOptions) (*redis.Client, error) {
	if options.Addr == "" {
		return nil, errors.New("redis addr is required")
	}
	if options.DB < 0 {
		return nil, errors.New("redis db must be non-negative")
	}
	redisOptions := &redis.Options{
		Addr:         options.Addr,
		Username:     options.Username,
		Password:     options.Password,
		DB:           options.DB,
		DialTimeout:  options.DialTimeout,
		ReadTimeout:  options.ReadTimeout,
		WriteTimeout: options.WriteTimeout,
	}
	if options.TLS {
		redisOptions.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	return redis.NewClient(redisOptions), nil
}

type redisKV struct {
	client      *redis.Client
	keyPrefix   string
	unavailable error
}

// newRedisKV keeps every key at keyPrefix + namespace + id.
func newRedisKV(client *redis.Client, keyPrefix, namespace string, unavailable error) (redisKV, error) {
	if client == nil {
		return redisKV{}, fmt.Errorf("%w: redis client is required", unavailable)
	}
	if keyPrefix == "" {
		return redisKV{}, fmt.Errorf("%w: redis key prefix is required", unavailable)
	}
	return redisKV{client: client, keyPrefix: keyPrefix + namespace, unavailable: unavailable}, nil
}

func (s redisKV) key(id string) string { return s.keyPrefix + id }

func (s redisKV) Ready(ctx context.Context) error {
	if err := s.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("%w: %w", s.unavailable, err)
	}
	return nil
}

func (s redisKV) logCorrupt(message, component, idAttr, id, reason string) {
	sum := sha256.Sum256([]byte(s.key(id)))
	slog.Error(message, "component", component, idAttr, id, "key_sha256", hex.EncodeToString(sum[:]), "reason", reason)
}
