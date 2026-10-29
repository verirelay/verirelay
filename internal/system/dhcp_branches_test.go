//go:build linux

package system

import (
	"errors"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
)

func validACKModifiers() []dhcpv4.Modifier {
	return []dhcpv4.Modifier{
		dhcpv4.WithYourIP(net.IPv4(192, 0, 2, 10)),
		dhcpv4.WithNetmask(net.IPMask(net.IPv4Mask(255, 255, 255, 0))),
		dhcpv4.WithRouter(net.IPv4(192, 0, 2, 1)),
		dhcpv4.WithDNS(net.IPv4(1, 1, 1, 1)),
		dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.IPv4(192, 0, 2, 254))),
		dhcpv4.WithOption(dhcpv4.OptIPAddressLeaseTime(time.Hour)),
	}
}

func ackWith(t *testing.T, extra ...dhcpv4.Modifier) *dhcpv4.DHCPv4 {
	t.Helper()
	return mustACK(t, append(validACKModifiers(), extra...)...)
}

func ackWithout(t *testing.T, code dhcpv4.OptionCode, extra ...dhcpv4.Modifier) *dhcpv4.DHCPv4 {
	t.Helper()
	ack := ackWith(t, extra...)
	ack.Options.Del(code)
	return ack
}

func TestLeaseFromACKErrorBranches(t *testing.T) {
	strict := DefaultConfig().DHCP
	offer, err := dhcpv4.New(append([]dhcpv4.Modifier{dhcpv4.WithMessageType(dhcpv4.MessageTypeOffer)}, validACKModifiers()...)...)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ack  *dhcpv4.DHCPv4
		want string
		is   error
	}{
		{name: "nil ack", ack: nil, want: "invalid DHCPv4 lease: missing ACK", is: ErrInvalidDHCPLease},
		{name: "not ack", ack: offer, want: "invalid DHCPv4 lease: not an ACK message: OFFER", is: ErrInvalidDHCPLease},
		{name: "unspecified address", ack: ackWith(t, dhcpv4.WithYourIP(net.IPv4zero)), want: "invalid DHCPv4 lease: invalid client address 0.0.0.0", is: ErrInvalidDHCPLease},
		{name: "loopback address", ack: ackWith(t, dhcpv4.WithYourIP(net.IPv4(127, 0, 0, 1))), want: "invalid DHCPv4 lease: invalid client address 127.0.0.1", is: ErrInvalidDHCPLease},
		{name: "missing mask", ack: ackWithout(t, dhcpv4.OptionSubnetMask), want: "invalid DHCPv4 lease: missing or invalid subnet mask", is: ErrInvalidDHCPLease},
		{name: "zero mask", ack: ackWith(t, dhcpv4.WithNetmask(net.IPv4Mask(0, 0, 0, 0))), want: "invalid DHCPv4 lease: missing or invalid subnet mask", is: ErrInvalidDHCPLease},
		{name: "invalid router", ack: ackWith(t, dhcpv4.WithRouter(net.IPv4(224, 0, 0, 1))), want: "invalid DHCPv4 lease: invalid router 224.0.0.1", is: ErrInvalidDHCPLease},
		{name: "missing router", ack: ackWithout(t, dhcpv4.OptionRouter), want: "invalid DHCPv4 lease: missing router option", is: ErrInvalidDHCPLease},
		{name: "invalid dns", ack: ackWith(t, dhcpv4.WithDNS(net.IPv4(1, 1, 1, 1), net.IPv4(127, 0, 0, 53))), want: "invalid DHCPv4 lease: invalid DNS 127.0.0.53", is: ErrInvalidDHCPLease},
		{name: "missing dns", ack: ackWithout(t, dhcpv4.OptionDomainNameServer), want: ErrDHCPDNSUnavailable.Error(), is: ErrDHCPDNSUnavailable},
		{name: "missing lease time", ack: ackWithout(t, dhcpv4.OptionIPAddressLeaseTime), want: "invalid DHCPv4 lease: invalid lease time", is: ErrInvalidDHCPLease},
		{name: "t1 not before t2", ack: ackWith(t, dhcpv4.WithOption(dhcpv4.OptRenewTimeValue(50*time.Minute)), dhcpv4.WithOption(dhcpv4.OptRebindingTimeValue(50*time.Minute))), want: "invalid DHCPv4 lease: invalid T1/T2/LeaseTime", is: ErrInvalidDHCPLease},
		{name: "t2 not before lease", ack: ackWith(t, dhcpv4.WithOption(dhcpv4.OptRebindingTimeValue(time.Hour))), want: "invalid DHCPv4 lease: invalid T1/T2/LeaseTime", is: ErrInvalidDHCPLease},
		{name: "missing server id", ack: ackWithout(t, dhcpv4.OptionServerIdentifier), want: "invalid DHCPv4 lease: missing or invalid server identifier", is: ErrInvalidDHCPLease},
		{name: "invalid server id", ack: ackWith(t, dhcpv4.WithOption(dhcpv4.OptServerIdentifier(net.IPv4(224, 0, 0, 1)))), want: "invalid DHCPv4 lease: missing or invalid server identifier", is: ErrInvalidDHCPLease},
		{name: "mtu length", ack: ackWith(t, dhcpv4.WithGeneric(dhcpv4.OptionInterfaceMTU, []byte{5})), want: "invalid DHCPv4 lease: invalid MTU option length", is: ErrInvalidDHCPLease},
		{name: "mtu range", ack: ackWith(t, dhcpv4.WithGeneric(dhcpv4.OptionInterfaceMTU, []byte{0, 1})), want: "invalid DHCPv4 lease: invalid MTU 1", is: ErrInvalidDHCPLease},
		{name: "address checked before router", ack: ackWith(t, dhcpv4.WithYourIP(net.IPv4zero), dhcpv4.WithRouter(net.IPv4zero)), want: "invalid DHCPv4 lease: invalid client address 0.0.0.0", is: ErrInvalidDHCPLease},
		{name: "router checked before dns", ack: ackWith(t, dhcpv4.WithRouter(net.IPv4zero), dhcpv4.WithDNS(net.IPv4zero)), want: "invalid DHCPv4 lease: invalid router 0.0.0.0", is: ErrInvalidDHCPLease},
		{name: "dns checked before lease time", ack: ackWithout(t, dhcpv4.OptionIPAddressLeaseTime, dhcpv4.WithDNS(net.IPv4zero)), want: "invalid DHCPv4 lease: invalid DNS 0.0.0.0", is: ErrInvalidDHCPLease},
		{name: "timers checked before server id", ack: ackWithout(t, dhcpv4.OptionServerIdentifier, dhcpv4.WithOption(dhcpv4.OptRebindingTimeValue(time.Hour))), want: "invalid DHCPv4 lease: invalid T1/T2/LeaseTime", is: ErrInvalidDHCPLease},
		{name: "server id checked before mtu", ack: ackWithout(t, dhcpv4.OptionServerIdentifier, dhcpv4.WithGeneric(dhcpv4.OptionInterfaceMTU, []byte{5})), want: "invalid DHCPv4 lease: missing or invalid server identifier", is: ErrInvalidDHCPLease},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lease, err := leaseFromACK("eth-test", tc.ack, strict)
			if err == nil || err.Error() != tc.want || !errors.Is(err, tc.is) {
				t.Fatalf("leaseFromACK() = %+v, %v; want error %q", lease, err, tc.want)
			}
			if !reflect.DeepEqual(lease, DHCPLease{}) {
				t.Fatalf("lease on error = %+v, want zero", lease)
			}
		})
	}
}

func TestLeaseFromACKFields(t *testing.T) {
	ack := ackWith(t,
		dhcpv4.WithNetmask(net.IPv4Mask(255, 255, 240, 0)),
		dhcpv4.WithDNS(net.IPv4(1, 1, 1, 1), net.IPv4(8, 8, 8, 8), net.IPv4(9, 9, 9, 9), net.IPv4zero),
		dhcpv4.WithGeneric(dhcpv4.OptionInterfaceMTU, []byte{0x05, 0xdc}),
	)
	lease, err := leaseFromACK("eth-test", ack, DefaultConfig().DHCP)
	if err != nil {
		t.Fatal(err)
	}
	want := DHCPLease{
		InterfaceName: "eth-test",
		Address:       netip.MustParseAddr("192.0.2.10"),
		PrefixLength:  20,
		Router:        netip.MustParseAddr("192.0.2.1"),
		DNSServers:    []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("9.9.9.9")},
		MTU:           1500,
		LeaseTime:     time.Hour,
		T1:            30 * time.Minute,
		T2:            time.Hour * 7 / 8,
	}
	if !reflect.DeepEqual(lease, want) {
		t.Fatalf("lease = %+v\nwant  %+v", lease, want)
	}
}

func TestLeaseFromACKOptionalRouterAndDNS(t *testing.T) {
	cfg := DefaultConfig().DHCP
	cfg.RequireRouter = false
	cfg.RequireDNS = false
	ack := ackWithout(t, dhcpv4.OptionRouter)
	ack.Options.Del(dhcpv4.OptionDomainNameServer)
	lease, err := leaseFromACK("eth-test", ack, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if lease.Router.IsValid() || len(lease.DNSServers) != 0 || lease.DNSServers == nil {
		t.Fatalf("router = %v dns = %#v; want zero router and empty non-nil dns", lease.Router, lease.DNSServers)
	}
}
