//go:build linux

package system

import (
	"net"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
)

func TestLeaseFromACKValid(t *testing.T) {
	ack := mustACK(t,
		dhcpv4.WithYourIP(net.IPv4(192, 0, 2, 10)),
		dhcpv4.WithNetmask(net.IPMask(net.IPv4Mask(255, 255, 255, 0))),
		dhcpv4.WithRouter(net.IPv4(192, 0, 2, 1)),
		dhcpv4.WithDNS(net.IPv4(1, 1, 1, 1), net.IPv4(8, 8, 8, 8), net.IPv4(9, 9, 9, 9), net.IPv4(4, 4, 4, 4)),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.IPv4(192, 0, 2, 1))),
		dhcpv4.WithLeaseTime(uint32((2 * time.Hour).Seconds())),
		dhcpv4.WithOption(dhcpv4.OptIPAddressLeaseTime(2*time.Hour)),
		dhcpv4.WithOption(dhcpv4.OptRenewTimeValue(30*time.Minute)),
		dhcpv4.WithOption(dhcpv4.OptRebindingTimeValue(90*time.Minute)),
	)

	lease, err := leaseFromACK("eth-test", ack, DefaultConfig().DHCP)
	if err != nil {
		t.Fatalf("leaseFromACK() error = %v", err)
	}
	if lease.Address.String() != "192.0.2.10" {
		t.Fatalf("address = %s", lease.Address)
	}
	if lease.PrefixLength != 24 {
		t.Fatalf("prefix = %d", lease.PrefixLength)
	}
	if len(lease.DNSServers) != 3 {
		t.Fatalf("dns count = %d", len(lease.DNSServers))
	}
	if lease.T1 != 30*time.Minute || lease.T2 != 90*time.Minute {
		t.Fatalf("timers = %s/%s", lease.T1, lease.T2)
	}
}

func TestLeaseFromACKRejectsMissingRouter(t *testing.T) {
	ack := mustACK(t,
		dhcpv4.WithYourIP(net.IPv4(192, 0, 2, 10)),
		dhcpv4.WithNetmask(net.IPMask(net.IPv4Mask(255, 255, 255, 0))),
		dhcpv4.WithDNS(net.IPv4(1, 1, 1, 1)),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.IPv4(192, 0, 2, 1))),
		dhcpv4.WithOption(dhcpv4.OptIPAddressLeaseTime(time.Hour)),
	)

	if _, err := leaseFromACK("eth-test", ack, DefaultConfig().DHCP); err == nil {
		t.Fatal("leaseFromACK() expected error")
	}
}

func TestLeaseFromACKRejectsMissingDNS(t *testing.T) {
	ack := mustACK(t,
		dhcpv4.WithYourIP(net.IPv4(192, 0, 2, 10)),
		dhcpv4.WithNetmask(net.IPMask(net.IPv4Mask(255, 255, 255, 0))),
		dhcpv4.WithRouter(net.IPv4(192, 0, 2, 1)),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.IPv4(192, 0, 2, 1))),
		dhcpv4.WithOption(dhcpv4.OptIPAddressLeaseTime(time.Hour)),
	)

	if _, err := leaseFromACK("eth-test", ack, DefaultConfig().DHCP); err == nil {
		t.Fatal("leaseFromACK() expected error")
	}
}

func TestLeaseFromACKRejectsInvalidTimers(t *testing.T) {
	ack := mustACK(t,
		dhcpv4.WithYourIP(net.IPv4(192, 0, 2, 10)),
		dhcpv4.WithNetmask(net.IPMask(net.IPv4Mask(255, 255, 255, 0))),
		dhcpv4.WithRouter(net.IPv4(192, 0, 2, 1)),
		dhcpv4.WithDNS(net.IPv4(1, 1, 1, 1)),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.IPv4(192, 0, 2, 1))),
		dhcpv4.WithOption(dhcpv4.OptIPAddressLeaseTime(time.Hour)),
		dhcpv4.WithOption(dhcpv4.OptRenewTimeValue(50*time.Minute)),
		dhcpv4.WithOption(dhcpv4.OptRebindingTimeValue(40*time.Minute)),
	)

	if _, err := leaseFromACK("eth-test", ack, DefaultConfig().DHCP); err == nil {
		t.Fatal("leaseFromACK() expected error")
	}
}

func TestLeaseFromACKRejectsInvalidMTU(t *testing.T) {
	ack := mustACK(t,
		dhcpv4.WithYourIP(net.IPv4(192, 0, 2, 10)),
		dhcpv4.WithNetmask(net.IPMask(net.IPv4Mask(255, 255, 255, 0))),
		dhcpv4.WithRouter(net.IPv4(192, 0, 2, 1)),
		dhcpv4.WithDNS(net.IPv4(1, 1, 1, 1)),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.IPv4(192, 0, 2, 1))),
		dhcpv4.WithOption(dhcpv4.OptIPAddressLeaseTime(time.Hour)),
		dhcpv4.WithGeneric(dhcpv4.OptionInterfaceMTU, []byte{0, 1}),
	)

	if _, err := leaseFromACK("eth-test", ack, DefaultConfig().DHCP); err == nil {
		t.Fatal("leaseFromACK() expected error")
	}
}

func mustACK(t *testing.T, modifiers ...dhcpv4.Modifier) *dhcpv4.DHCPv4 {
	t.Helper()
	mods := []dhcpv4.Modifier{dhcpv4.WithMessageType(dhcpv4.MessageTypeAck)}
	mods = append(mods, modifiers...)
	ack, err := dhcpv4.New(mods...)
	if err != nil {
		t.Fatalf("dhcpv4.New() error = %v", err)
	}
	return ack
}
