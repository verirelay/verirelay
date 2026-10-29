//go:build linux

package system

import (
	"context"
	"fmt"
	"log/slog"
)

// Init performs system-level initialization: mounting filesystems, loading
// kernel modules, and configuring the DHCPv4 network.
func Init(cfg Config) error {
	mountProc(cfg.ProcPath)
	mountDev(cfg.DevPath)

	loadModules()

	slog.Info("configuring VM DHCPv4 network topology", "component", "system")
	if err := configureLoopback(cfg.LoopbackName); err != nil {
		return fmt.Errorf("configure loopback interface: %w", err)
	}
	if err := configureDHCPv4Network(context.Background(), cfg); err != nil {
		return fmt.Errorf("configure DHCPv4 network: %w", err)
	}

	return nil
}
