package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	redis "github.com/redis/go-redis/v9"
)

// RedisRawStore keeps Raw Captures at <key_prefix>raw:<store_id>.
type RedisRawStore struct {
	redisKV
}

func NewRedisRawStore(client *redis.Client, keyPrefix string) (*RedisRawStore, error) {
	kv, err := newRedisKV(client, keyPrefix, rawKeyNamespace, ErrRawStoreUnavailable)
	if err != nil {
		return nil, err
	}
	return &RedisRawStore{redisKV: kv}, nil
}

func (s *RedisRawStore) Save(ctx context.Context, storeID string, data []byte, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ttl < time.Second {
		return ErrRawStoreUnavailable
	}
	if err := s.client.Set(ctx, s.key(storeID), data, ttl.Truncate(time.Second)).Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrRawStoreUnavailable, err)
	}
	return nil
}

func (s *RedisRawStore) Get(ctx context.Context, storeID string) ([]byte, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	data, err := s.client.Get(ctx, s.key(storeID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrRawStoreUnavailable, err)
	}
	return data, true, nil
}

func (s *RedisRawStore) Delete(ctx context.Context, storeID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.client.Del(ctx, s.key(storeID)).Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrRawStoreUnavailable, err)
	}
	return nil
}
