//go:build linux

package system

import (
	"log/slog"
	"os"
	"syscall"
)

func mountProc(procPath string) {
	if procPath == "" {
		procPath = "/proc"
	}
	if err := os.MkdirAll(procPath, 0o755); err != nil { //nolint:gosec // G301: PID 1 in the initrd mounts /proc itself; it must be 0755
		slog.Warn("failed to create proc directory", "component", "system", "path", procPath, "err", err)
		return
	}
	if err := syscall.Mount("proc", procPath, "proc", 0, ""); err != nil {
		slog.Warn("failed to mount proc", "component", "system", "path", procPath, "err", err)
	}
}

func mountDev(devPath string) {
	_ = os.MkdirAll(devPath, 0o755) //nolint:gosec // G301: PID 1 in the initrd mounts /dev itself; it must be 0755
	err := syscall.Mount("devtmpfs", devPath, "devtmpfs", 0, "")
	if err != nil {
		slog.Warn("failed to mount dev", "component", "system", "path", devPath, "err", err)
	}
}
