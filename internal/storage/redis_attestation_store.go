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

const redisAttestationRecordVersion = "verirelay-attestation-store/1"

// RedisAttestationStore keeps archived attestation responses at
// <key_prefix>attestation:<key_id>.
type RedisAttestationStore struct {
	redisKV
}

type redisAttestationRecord struct {
	Version     string                   `json:"version"`
	Attestation avr1.AttestationResponse `json:"attestation"`
}

func NewRedisAttestationStore(client *redis.Client, keyPrefix string) (*RedisAttestationStore, error) {
	kv, err := newRedisKV(client, keyPrefix, attestationKeyNamespace, ErrAttestationStoreUnavailable)
	if err != nil {
		return nil, err
	}
	return &RedisAttestationStore{redisKV: kv}, nil
}

func (s *RedisAttestationStore) Save(ctx context.Context, keyID string, attestation avr1.AttestationResponse, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if ttl <= 0 {
		return fmt.Errorf("%w: ttl must be positive", ErrAttestationStoreUnavailable)
	}
	if err := validateAttestation(keyID, attestation); err != nil {
		return err
	}
	data, err := json.Marshal(redisAttestationRecord{Version: redisAttestationRecordVersion, Attestation: attestation})
	if err != nil {
		return fmt.Errorf("%w: marshal attestation: %w", ErrInvalidAttestation, err)
	}
	if err := s.client.Set(ctx, s.key(keyID), data, ttl).Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrAttestationStoreUnavailable, err)
	}
	return nil
}

func (s *RedisAttestationStore) Get(ctx context.Context, keyID string) (avr1.AttestationResponse, bool, error) {
	if err := ctx.Err(); err != nil {
		return avr1.AttestationResponse{}, false, err
	}
	data, err := s.client.Get(ctx, s.key(keyID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return avr1.AttestationResponse{}, false, nil
	}
	if err != nil {
		return avr1.AttestationResponse{}, false, fmt.Errorf("%w: %w", ErrAttestationStoreUnavailable, err)
	}
	var record redisAttestationRecord
	if err := json.Unmarshal(data, &record); err != nil {
		s.logCorrupt("corrupt attestation record", "attestation_store", "key_id", keyID, "invalid_json")
		return avr1.AttestationResponse{}, false, fmt.Errorf("%w: invalid json", ErrCorruptAttestationRecord)
	}
	if record.Version != redisAttestationRecordVersion {
		s.logCorrupt("corrupt attestation record", "attestation_store", "key_id", keyID, "invalid_version")
		return avr1.AttestationResponse{}, false, fmt.Errorf("%w: invalid version", ErrCorruptAttestationRecord)
	}
	if err := validateAttestation(keyID, record.Attestation); err != nil {
		s.logCorrupt("corrupt attestation record", "attestation_store", "key_id", keyID, "invalid_attestation")
		return avr1.AttestationResponse{}, false, fmt.Errorf("%w: invalid attestation", ErrCorruptAttestationRecord)
	}
	return record.Attestation, true, nil
}
