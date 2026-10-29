package system

import (
	"log/slog"

	"golang.org/x/sys/unix"
)

func kernelModulesRoot() string {
	var uts unix.Utsname
	if err := unix.Uname(&uts); err != nil {
		slog.Warn("failed to determine kernel version", "component", "system", "err", err)
		return "/lib/modules"
	}

	release := unix.ByteSliceToString(uts.Release[:])
	if release == "" {
		slog.Warn("failed to determine kernel version", "component", "system")
		return "/lib/modules"
	}

	return "/lib/modules/" + release
}
