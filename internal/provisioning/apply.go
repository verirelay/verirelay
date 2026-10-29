package provisioning

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/verirelay/protocol/guest"

	"github.com/verirelay/verirelay/internal/attestation"
	"github.com/verirelay/verirelay/internal/config"
)

// Guest is what Provisioning needs from the platform.
type Guest struct {
	// ReadConfigDisk returns the start of the config disk.
	ReadConfigDisk func() ([]byte, error)
	Reporter       Reporter
	// HTTPClient reaches VeriProv; nil means NewHTTPClient.
	HTTPClient *http.Client
}

// Resolve obtains the Runtime Config and returns cfg with it applied. A
// production Guest reads its Bootstrap from the config disk, checks it against
// HOST_DATA and provisions from the VeriProv it names. In development mode the
// Runtime Config comes from the local source, from VeriProv, or not at all.
func Resolve(ctx context.Context, cfg config.Config, platform Guest) (config.Config, error) {
	if cfg.Production {
		return resolveProduction(ctx, cfg, platform)
	}
	switch {
	case len(cfg.Provisioning.RuntimeConfig) > 0:
		runtimeConfig, err := decodeRuntimeConfig(cfg.Provisioning.RuntimeConfig)
		if err != nil {
			return config.Config{}, fmt.Errorf("%w: %w", config.ErrInvalidRuntimeConfig, err)
		}
		return config.ApplyRuntimeConfig(cfg, runtimeConfig)
	case cfg.Provisioning.VeriProvURL != "":
		baseURL, err := url.Parse(cfg.Provisioning.VeriProvURL)
		if err != nil {
			return config.Config{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
		}
		return provision(ctx, cfg, platform, baseURL, cfg.Provisioning.IdentityPublicKey)
	default:
		return cfg, nil
	}
}

func resolveProduction(ctx context.Context, cfg config.Config, platform Guest) (config.Config, error) {
	disk, err := platform.ReadConfigDisk()
	if err != nil {
		return config.Config{}, fmt.Errorf("%w: %w", ErrInvalidBootstrap, err)
	}
	file, bootstrap, err := readBootstrap(disk)
	if err != nil {
		return config.Config{}, err
	}
	if err := checkHostData(ctx, platform.Reporter, file); err != nil {
		return config.Config{}, err
	}
	slog.Info("bootstrap matches HOST_DATA", "component", "provisioning", "veriprov", bootstrap.URL, "profile", bootstrap.Profile)
	identity, err := bootstrap.IdentityKey()
	if err != nil {
		return config.Config{}, fmt.Errorf("%w: %w", ErrInvalidBootstrap, err)
	}
	baseURL, err := url.Parse(bootstrap.URL)
	if err != nil {
		return config.Config{}, fmt.Errorf("%w: %w", ErrInvalidBootstrap, err)
	}
	effective, err := provision(ctx, cfg, platform, baseURL, identity)
	if err != nil {
		return config.Config{}, err
	}
	effective.Bootstrap = bytes.Clone(file)
	return effective, nil
}

// readBootstrap takes the Bootstrap file from the start of the config disk
// and decodes and validates it.
func readBootstrap(disk []byte) ([]byte, guest.Bootstrap, error) {
	file, err := guest.BootstrapFromConfigDisk(disk)
	if err != nil {
		return nil, guest.Bootstrap{}, fmt.Errorf("%w: %w", ErrInvalidBootstrap, err)
	}
	bootstrap, err := guest.DecodeBootstrap(file)
	if err != nil {
		return nil, guest.Bootstrap{}, fmt.Errorf("%w: %w", ErrInvalidBootstrap, err)
	}
	if err := bootstrap.Validate(); err != nil {
		return nil, guest.Bootstrap{}, fmt.Errorf("%w: %w", ErrInvalidBootstrap, err)
	}
	return file, bootstrap, nil
}

// checkHostData requires the HOST_DATA of the Guest's own Attestation Report
// to be the SHA-256 of the Bootstrap file.
func checkHostData(ctx context.Context, reporter Reporter, bootstrap []byte) error {
	if reporter == nil {
		return ErrReportUnavailable
	}
	report, err := reporter.Report(ctx, [64]byte{})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrReportUnavailable, err)
	}
	hostData, err := attestation.HostData(report)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrReportUnavailable, err)
	}
	if want := guest.HostData(bootstrap); !bytes.Equal(hostData, want[:]) {
		return fmt.Errorf("%w: HOST_DATA is %x, Bootstrap SHA-256 is %x", ErrHostDataMismatch, hostData, want)
	}
	return nil
}

func provision(ctx context.Context, cfg config.Config, platform Guest, baseURL *url.URL, identity ed25519.PublicKey) (config.Config, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.Provisioning.Timeout)
	defer cancel()
	client := &Client{BaseURL: baseURL, HTTPClient: platform.HTTPClient, Reporter: platform.Reporter, IdentityKey: identity}
	runtimeConfig, err := client.FetchRuntimeConfig(ctx)
	if err != nil {
		return config.Config{}, err
	}
	return config.ApplyRuntimeConfig(cfg, runtimeConfig)
}
