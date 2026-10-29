//go:build linux

package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/verirelay/verirelay/internal/attestation"
	"github.com/verirelay/verirelay/internal/attestation/sevsnp"
	"github.com/verirelay/verirelay/internal/config"
	"github.com/verirelay/verirelay/internal/provisioning"
	"github.com/verirelay/verirelay/internal/server"
	"github.com/verirelay/verirelay/internal/system"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	slog.Info("starting system initialization", "component", "system")
	systemConfig := system.DefaultConfig()
	if err := system.Init(systemConfig); err != nil {
		slog.Error("system init failed", "component", "system", "err", err)
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("load config failed", "component", "config", "err", err)
		os.Exit(1)
	}
	slog.Info("configuration loaded", "component", "config", "production", cfg.Production)

	reporter := provisioning.Reporter(sevsnp.NewProvider())
	if cfg.Provisioning.FakeQuote {
		reporter = attestation.FakeProvider{}
	}
	platform := provisioning.Guest{
		ReadConfigDisk: func() ([]byte, error) { return system.ReadConfigDisk(systemConfig.DevPath) },
		Reporter:       reporter,
	}
	gate := server.NewGate()
	go server.Start(context.Background(), cfg, platform, gate)

	slog.Info("VeriRelay listening", "component", "server", "addr", cfg.ListenAddr)
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.LoggingMiddleware(gate),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		slog.Error("serve failed", "component", "server", "err", err)
		os.Exit(1)
	}
}
