//go:build linux

package system

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
)

type fakeLinks struct {
	byName map[string]netlink.Link
	list   []netlink.Link

	setMTUCalled       bool
	addrReplaceCalled  bool
	routeReplaceCalled bool
}

func (f *fakeLinks) LinkByName(name string) (netlink.Link, error) {
	link, ok := f.byName[name]
	if !ok {
		return nil, netlink.LinkNotFoundError{}
	}
	return link, nil
}

func (f *fakeLinks) LinkList() ([]netlink.Link, error) { return f.list, nil }

func (f *fakeLinks) LinkSetMTU(netlink.Link, int) error {
	f.setMTUCalled = true
	return nil
}

func (f *fakeLinks) AddrReplace(netlink.Link, *netlink.Addr) error {
	f.addrReplaceCalled = true
	return nil
}

func (f *fakeLinks) RouteReplace(*netlink.Route) error {
	f.routeReplaceCalled = true
	return nil
}
func (f *fakeLinks) AddrDel(netlink.Link, *netlink.Addr) error { return nil }
func (f *fakeLinks) RouteDel(*netlink.Route) error             { return nil }

func TestSelectEthernetLinkByName(t *testing.T) {
	eth := testLink("eth-test", 2, 0)
	links := &fakeLinks{byName: map[string]netlink.Link{"eth-test": eth}}

	got, err := selectEthernetLink(links, "eth-test")
	if err != nil {
		t.Fatalf("selectEthernetLink() error = %v", err)
	}
	if got.Attrs().Name != "eth-test" {
		t.Fatalf("selected link = %s", got.Attrs().Name)
	}
}

func TestSelectEthernetLinkRejectsLoopback(t *testing.T) {
	lo := testLink("lo", 1, net.FlagLoopback)
	links := &fakeLinks{byName: map[string]netlink.Link{"lo": lo}}

	if _, err := selectEthernetLink(links, "lo"); err == nil {
		t.Fatal("selectEthernetLink() expected error")
	}
}

func TestSelectEthernetLinkRejectsAmbiguousAutoDetect(t *testing.T) {
	links := &fakeLinks{list: []netlink.Link{
		testLink("eth0", 2, 0),
		testLink("eth1", 3, 0),
	}}

	if _, err := selectEthernetLink(links, ""); err == nil {
		t.Fatal("selectEthernetLink() expected error")
	}
}

func TestApplyDHCPLease(t *testing.T) {
	links := &fakeLinks{}
	link := testLink("eth-test", 2, 0)
	cfg := DefaultConfig()
	cfg.ResolvConfPath = t.TempDir() + "/resolv.conf"
	lease := DHCPLease{
		InterfaceName: "eth-test",
		Address:       netip.MustParseAddr("192.0.2.10"),
		PrefixLength:  24,
		Router:        netip.MustParseAddr("192.0.2.1"),
		DNSServers:    []netip.Addr{netip.MustParseAddr("1.1.1.1")},
		MTU:           1500,
		LeaseTime:     time.Hour,
		T1:            30 * time.Minute,
		T2:            45 * time.Minute,
	}

	if err := applyDHCPLease(links, link, cfg, lease); err != nil {
		t.Fatalf("applyDHCPLease() error = %v", err)
	}
	if !links.setMTUCalled || !links.addrReplaceCalled || !links.routeReplaceCalled {
		t.Fatalf("expected netlink calls, got mtu=%t addr=%t route=%t", links.setMTUCalled, links.addrReplaceCalled, links.routeReplaceCalled)
	}
}

func testLink(name string, index int, flags net.Flags) netlink.Link {
	attrs := netlink.NewLinkAttrs()
	attrs.Name = name
	attrs.Index = index
	attrs.Flags = flags
	return &netlink.Dummy{LinkAttrs: attrs}
}
