//go:build linux

package system

import (
	"net/netip"
	"strings"
	"testing"
)

func TestRenderResolvConf(t *testing.T) {
	content, err := renderResolvConf([]netip.Addr{
		netip.MustParseAddr("1.1.1.1"),
		netip.MustParseAddr("8.8.8.8"),
		netip.MustParseAddr("9.9.9.9"),
		netip.MustParseAddr("4.4.4.4"),
	})
	if err != nil {
		t.Fatalf("renderResolvConf() error = %v", err)
	}
	if strings.Count(content, "nameserver ") != 3 {
		t.Fatalf("nameserver count mismatch:\n%s", content)
	}
	if strings.Contains(content, "search") || strings.Contains(content, "domain") {
		t.Fatalf("resolv.conf must not include DHCP search/domain:\n%s", content)
	}
}

func TestRenderResolvConfRejectsEmptyDNS(t *testing.T) {
	if _, err := renderResolvConf(nil); err == nil {
		t.Fatal("renderResolvConf() expected error")
	}
}

func TestWriteResolvConf(t *testing.T) {
	path := t.TempDir() + "/resolv.conf"
	if err := writeResolvConf(path, []netip.Addr{netip.MustParseAddr("1.1.1.1")}); err != nil {
		t.Fatalf("writeResolvConf() error = %v", err)
	}
}
