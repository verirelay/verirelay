package server

import (
	"context"
	"fmt"

	redis "github.com/redis/go-redis/v9"
	"github.com/verirelay/protocol/avr1"

	"github.com/verirelay/verirelay/internal/attestation"
	"github.com/verirelay/verirelay/internal/attestation/sevsnp"
	"github.com/verirelay/verirelay/internal/config"
	"github.com/verirelay/verirelay/internal/keys"
	"github.com/verirelay/verirelay/internal/proxy"
	"github.com/verirelay/verirelay/internal/storage"
)

// New builds the proxy for an effective configuration: it creates the
// Receipt Signing Key and the stores, and archives the startup attestation
// response.
func New(ctx context.Context, cfg config.Config) (handler *proxy.Handler, err error) {
	signer, err := keys.NewReceiptSigner(cfg.ReceiptKeySeed)
	if err != nil {
		return nil, err
	}
	stores, err := newStores(cfg)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			stores.close()
		}
	}()
	quoteProvider := attestation.QuoteProvider(sevsnp.NewProvider())
	if cfg.FakeAttestation {
		quoteProvider = attestation.FakeProvider{}
	}
	manager := attestation.NewManagerWithOptions(quoteProvider, attestation.ManagerOptions{
		DisclosureReader: attestation.NewFileDisclosureReader(),
		Bootstrap:        cfg.Bootstrap,
	})
	archived, err := startupAttestation(ctx, manager, signer)
	if err != nil {
		return nil, err
	}
	archivedTTL := storage.ArchivedAttestationTTL(cfg.ReceiptStore.TTL)
	if err := stores.attestations.Save(ctx, signer.KeyID(), archived, archivedTTL); err != nil {
		return nil, fmt.Errorf("save startup attestation: %w", err)
	}
	return proxy.NewHandler(cfg.DebugErrors, proxy.Options{
		AllowPrivateUpstreams:  cfg.AllowPrivateUpstreams,
		AdminToken:             cfg.AdminToken,
		AttestationManager:     manager,
		ReceiptSigner:          signer,
		ReceiptStore:           stores.receipts,
		AttestationStore:       stores.attestations,
		ArchivedAttestation:    &archived,
		ArchivedAttestationTTL: archivedTTL,
		RawStore:               stores.raw,
		RawStoreMaxTTL:         cfg.RawStore.MaxTTL,
	}), nil
}

func startupAttestation(ctx context.Context, manager *attestation.Manager, signer *avr1.ReceiptSignerV1) (avr1.AttestationResponse, error) {
	publicKey, err := avr1.DecodeEd25519PublicKeyBase64URL(signer.PublicKeyBase64URL())
	if err != nil {
		return avr1.AttestationResponse{}, fmt.Errorf("decode receipt signing public key: %w", err)
	}
	response, err := manager.Attestation(ctx, attestation.Request{
		Nonce:            make([]byte, avr1.AVRNonceBytes),
		ReceiptPublicKey: publicKey,
		KeyID:            signer.KeyID(),
	})
	if err != nil {
		return avr1.AttestationResponse{}, fmt.Errorf("generate startup attestation: %w", err)
	}
	return response, nil
}

type stores struct {
	receipts     storage.ReceiptStore
	attestations storage.AttestationStore
	raw          storage.RawStore
	clients      []*redis.Client
}

// newStores builds the Receipt, archived attestation and Raw Capture stores.
// The attestation store shares the Receipt store's backend, and a Redis Raw
// Capture store with the Receipt store's connection settings shares its client.
func newStores(cfg config.Config) (s stores, err error) {
	defer func() {
		if err != nil {
			s.close()
		}
	}()
	var receiptClient *redis.Client
	switch cfg.ReceiptStore.Type {
	case config.StoreMemory:
		s.receipts = storage.NewMemoryReceiptStoreWithOptions(storage.MemoryReceiptStoreOptions{TTL: cfg.ReceiptStore.TTL, MaxEntries: cfg.ReceiptStore.MaxEntries})
		s.attestations = storage.NewMemoryAttestationStore()
	case config.StoreRedis:
		if receiptClient, err = s.redisClient(cfg.ReceiptStore.Redis); err != nil {
			return s, fmt.Errorf("%w: %w", config.ErrInvalidReceiptStore, err)
		}
		if s.receipts, err = storage.NewRedisReceiptStore(receiptClient, cfg.ReceiptStore.KeyPrefix, cfg.ReceiptStore.TTL); err != nil {
			return s, err
		}
		if s.attestations, err = storage.NewRedisAttestationStore(receiptClient, cfg.ReceiptStore.KeyPrefix); err != nil {
			return s, err
		}
	default:
		return s, fmt.Errorf("%w: unsupported type %q", config.ErrInvalidReceiptStore, cfg.ReceiptStore.Type)
	}
	switch cfg.RawStore.Type {
	case config.RawStoreDisabled:
	case config.StoreMemory:
		s.raw = storage.NewMemoryRawStoreWithOptions(storage.MemoryRawStoreOptions{MaxBytes: cfg.RawStore.MaxBytes})
	case config.StoreRedis:
		client := receiptClient
		if client == nil || cfg.RawStore.Redis != cfg.ReceiptStore.Redis {
			if client, err = s.redisClient(cfg.RawStore.Redis); err != nil {
				return s, fmt.Errorf("%w: %w", config.ErrInvalidRawStore, err)
			}
		}
		if s.raw, err = storage.NewRedisRawStore(client, cfg.RawStore.KeyPrefix); err != nil {
			return s, err
		}
	default:
		return s, fmt.Errorf("%w: unsupported type %q", config.ErrInvalidRawStore, cfg.RawStore.Type)
	}
	return s, nil
}

func (s *stores) redisClient(cfg config.RedisConfig) (*redis.Client, error) {
	client, err := storage.NewRedisClient(storage.RedisOptions{
		Addr:         cfg.Addr,
		TLS:          cfg.TLS,
		Username:     cfg.Username,
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})
	if err != nil {
		return nil, err
	}
	s.clients = append(s.clients, client)
	return client, nil
}

func (s *stores) close() {
	for _, client := range s.clients {
		_ = client.Close()
	}
}
