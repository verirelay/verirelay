package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	redis "github.com/redis/go-redis/v9"
	"github.com/verirelay/protocol/avr1"
)

const redisReceiptRecordVersion = "verirelay-receipt-store/1"

// RedisReceiptStore keeps Receipts at <key_prefix>receipt:<request_id>.
type RedisReceiptStore struct {
	redisKV
	ttl time.Duration
}

type redisReceiptRecord struct {
	Version string               `json:"version"`
	Receipt avr1.SignedReceiptV1 `json:"receipt"`
}

func NewRedisReceiptStore(client *redis.Client, keyPrefix string, ttl time.Duration) (*RedisReceiptStore, error) {
	if ttl <= 0 {
		return nil, fmt.Errorf("%w: redis receipt ttl must be positive", ErrStoreUnavailable)
	}
	kv, err := newRedisKV(client, keyPrefix, receiptKeyNamespace, ErrStoreUnavailable)
	if err != nil {
		return nil, err
	}
	return &RedisReceiptStore{redisKV: kv, ttl: ttl}, nil
}

func (s *RedisReceiptStore) Save(ctx context.Context, requestID string, receipt avr1.SignedReceiptV1) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateReceipt(requestID, receipt); err != nil {
		return err
	}
	data, err := json.Marshal(redisReceiptRecord{Version: redisReceiptRecordVersion, Receipt: receipt})
	if err != nil {
		return fmt.Errorf("%w: marshal receipt: %w", ErrInvalidReceipt, err)
	}
	if err := s.client.Set(ctx, s.key(requestID), data, s.ttl).Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrStoreUnavailable, err)
	}
	return nil
}

func (s *RedisReceiptStore) Get(ctx context.Context, requestID string) (avr1.SignedReceiptV1, bool, error) {
	if err := ctx.Err(); err != nil {
		return avr1.SignedReceiptV1{}, false, err
	}
	data, err := s.client.Get(ctx, s.key(requestID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return avr1.SignedReceiptV1{}, false, nil
	}
	if err != nil {
		return avr1.SignedReceiptV1{}, false, fmt.Errorf("%w: %w", ErrStoreUnavailable, err)
	}
	var record redisReceiptRecord
	if err := json.Unmarshal(data, &record); err != nil {
		s.logCorrupt("corrupt receipt record", "receipt_store", "request_id", requestID, "invalid_json")
		return avr1.SignedReceiptV1{}, false, fmt.Errorf("%w: invalid json", ErrCorruptRecord)
	}
	if record.Version != redisReceiptRecordVersion {
		s.logCorrupt("corrupt receipt record", "receipt_store", "request_id", requestID, "invalid_version")
		return avr1.SignedReceiptV1{}, false, fmt.Errorf("%w: invalid version", ErrCorruptRecord)
	}
	receipt := record.Receipt
	if receipt.RequestID != requestID || receipt.Signature == nil || avr1.VerifySignedReceiptV1(receipt) != nil {
		s.logCorrupt("corrupt receipt record", "receipt_store", "request_id", requestID, "invalid_receipt")
		return avr1.SignedReceiptV1{}, false, ErrCorruptRecord
	}
	return cloneSignedReceiptV1(receipt), true, nil
}
