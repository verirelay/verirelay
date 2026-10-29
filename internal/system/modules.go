//go:build linux

package system

import (
	"log/slog"
	"os"
	"path"
	"strings"

	"golang.org/x/sys/unix"
)

func loadModules() {
	modulesDir := kernelModulesRoot()
	orderPath := path.Join(modulesDir, "modules.order")

	orderBytes, err := os.ReadFile(orderPath)
	if err != nil {
		slog.Warn("failed to read module load order", "component", "system", "path", orderPath, "err", err)
		return
	}

	for mod := range strings.SplitSeq(string(orderBytes), "\n") {
		mod = strings.TrimSpace(mod)
		if mod == "" {
			continue
		}

		modulePath := path.Join(modulesDir, strings.TrimPrefix(mod, "/"))

		b, err := os.ReadFile(modulePath)
		if err != nil {
			slog.Warn("failed to read kernel module", "component", "system", "path", modulePath, "err", err)
			continue
		}

		if err := unix.InitModule(b, ""); err != nil {
			slog.Warn("failed to inject kernel module", "component", "system", "module", mod, "err", err)
		} else {
			slog.Info("injected dependency kernel module in order", "component", "system", "module", mod)
		}
	}
}
