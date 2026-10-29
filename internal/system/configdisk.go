package system

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// configDiskReadSize is how much of the config disk is read; the Bootstrap
// and its NUL padding start the device.
const configDiskReadSize = 64 << 10

// virtioDiskName matches whole virtio block devices, not their partitions.
var virtioDiskName = regexp.MustCompile(`^vd[a-z]+$`)

// ReadConfigDisk returns the first 64 KiB of the config disk, which must be
// the only virtio block device in devPath. A smaller device is read whole.
func ReadConfigDisk(devPath string) ([]byte, error) {
	entries, err := os.ReadDir(devPath)
	if err != nil {
		return nil, fmt.Errorf("list block devices: %w", err)
	}
	var disks []string
	for _, entry := range entries {
		if virtioDiskName.MatchString(entry.Name()) {
			disks = append(disks, entry.Name())
		}
	}
	if len(disks) != 1 {
		return nil, fmt.Errorf("want exactly one virtio block device in %s, found %d %v", devPath, len(disks), disks)
	}
	path := filepath.Join(devPath, disks[0])
	disk, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config disk: %w", err)
	}
	defer func() { _ = disk.Close() }()
	data, err := io.ReadAll(io.LimitReader(disk, configDiskReadSize))
	if err != nil {
		return nil, fmt.Errorf("read config disk %s: %w", path, err)
	}
	return data, nil
}
