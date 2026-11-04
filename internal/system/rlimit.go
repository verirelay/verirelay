//go:build linux

package system

import (
	"log/slog"

	"golang.org/x/sys/unix"
)

// openFileLimit is the open file limit PID 1 sets for itself. The kernel
// starts it at 4096, which would hold the server to 1024 client connections.
const openFileLimit = 1 << 16

func raiseOpenFileLimit() {
	limit := unix.Rlimit{Cur: openFileLimit, Max: openFileLimit}
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		slog.Warn("failed to raise the open file limit", "component", "system", "limit", openFileLimit, "err", err)
	}
}
