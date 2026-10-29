package system

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadConfigDiskReadsTheOnlyVirtioDisk(t *testing.T) {
	dev := t.TempDir()
	disk := append([]byte(`{"version":"veriprov-bootstrap/1"}`), make([]byte, 128<<10)...)
	writeDevice(t, dev, "vda", disk)
	for _, name := range []string{"vda1", "sda", "null", "vd"} {
		writeDevice(t, dev, name, []byte("not the config disk"))
	}
	got, err := ReadConfigDisk(dev)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, disk[:configDiskReadSize]) {
		t.Fatalf("read %d bytes, want the first %d bytes of the disk", len(got), configDiskReadSize)
	}
}

func TestReadConfigDiskReadsASmallDiskWhole(t *testing.T) {
	dev := t.TempDir()
	disk := append([]byte(`{}`), make([]byte, 4094)...)
	writeDevice(t, dev, "vdb", disk)
	got, err := ReadConfigDisk(dev)
	if err != nil || !bytes.Equal(got, disk) {
		t.Fatalf("ReadConfigDisk = %d bytes, %v", len(got), err)
	}
}

func TestReadConfigDiskRequiresExactlyOneVirtioDisk(t *testing.T) {
	for _, names := range [][]string{nil, {"vda", "vdb"}} {
		dev := t.TempDir()
		for _, name := range names {
			writeDevice(t, dev, name, []byte("{}\x00"))
		}
		if _, err := ReadConfigDisk(dev); err == nil || !strings.Contains(err.Error(), "exactly one virtio block device") {
			t.Fatalf("devices %v: err = %v", names, err)
		}
	}
}

func writeDevice(t *testing.T, dev, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dev, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
