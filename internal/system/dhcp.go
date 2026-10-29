//go:build linux

package system

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/insomniacslk/dhcp/dhcpv4"
	"github.com/insomniacslk/dhcp/dhcpv4/nclient4"
)

var (
	ErrDHCPDisabled       = errors.New("DHCPv4 initialization is disabled")
	ErrDHCPFailed         = errors.New("DHCPv4 request failed")
	ErrInvalidDHCPLease   = errors.New("invalid DHCPv4 lease")
	ErrDHCPDNSUnavailable = errors.New("DHCPv4 lease provides no usable DNS")
)

const (
	maxDNSServers = 3
	minDHCPMTU    = 576
	maxDHCPMTU    = 9000
)

// DHCPLease is the minimal internal DHCPv4 lease model used by VeriRelay.
type DHCPLease struct {
	InterfaceName string
	Address       netip.Addr
	PrefixLength  int
	Router        netip.Addr
	DNSServers    []netip.Addr
	MTU           int
	LeaseTime     time.Duration
	T1            time.Duration
	T2            time.Duration

	raw *nclient4.Lease
}

type dhcpClient interface {
	Renew(ctx context.Context, lease DHCPLease, cfg DHCPConfig) (DHCPLease, error)
	Rebind(ctx context.Context, lease DHCPLease, cfg DHCPConfig) (DHCPLease, error)
	Close() error
}

type insomniacDHCPClient struct {
	client *nclient4.Client
}

func newInsomniacDHCPClient(ifName string, cfg DHCPConfig) (*insomniacDHCPClient, error) {
	opts := []nclient4.ClientOpt{
		nclient4.WithTimeout(dhcpTimeout(cfg)),
		nclient4.WithRetry(dhcpRetries(cfg)),
	}
	client, err := nclient4.New(ifName, opts...)
	if err != nil {
		return nil, fmt.Errorf("create DHCPv4 client: %w", err)
	}
	return &insomniacDHCPClient{client: client}, nil
}

func (c *insomniacDHCPClient) Acquire(ctx context.Context, ifName string, cfg DHCPConfig) (DHCPLease, error) {
	lease, err := c.client.Request(ctx, dhcpRequestedOptions()...)
	if err != nil {
		return DHCPLease{}, fmt.Errorf("%w: %w", ErrDHCPFailed, err)
	}
	return leaseFromPacket(ifName, lease, cfg)
}

func (c *insomniacDHCPClient) Renew(ctx context.Context, current DHCPLease, cfg DHCPConfig) (DHCPLease, error) {
	lease, err := c.client.Renew(ctx, current.raw, dhcpRequestedOptions()...)
	if err != nil {
		return DHCPLease{}, fmt.Errorf("%w: %w", ErrDHCPFailed, err)
	}
	return leaseFromPacket(current.InterfaceName, lease, cfg)
}

func (c *insomniacDHCPClient) Rebind(ctx context.Context, current DHCPLease, cfg DHCPConfig) (DHCPLease, error) {
	lease, err := c.client.Request(ctx, dhcpRequestedOptions()...)
	if err != nil {
		return DHCPLease{}, fmt.Errorf("%w: %w", ErrDHCPFailed, err)
	}
	return leaseFromPacket(current.InterfaceName, lease, cfg)
}

func (c *insomniacDHCPClient) Close() error {
	if c.client == nil {
		return nil
	}
	return c.client.Close()
}

func dhcpRequestedOptions() []dhcpv4.Modifier {
	return []dhcpv4.Modifier{dhcpv4.WithRequestedOptions(
		dhcpv4.OptionSubnetMask,
		dhcpv4.OptionRouter,
		dhcpv4.OptionDomainNameServer,
		dhcpv4.OptionInterfaceMTU,
		dhcpv4.OptionIPAddressLeaseTime,
		dhcpv4.OptionRenewTimeValue,
		dhcpv4.OptionRebindingTimeValue,
	)}
}

func leaseFromPacket(ifName string, raw *nclient4.Lease, cfg DHCPConfig) (DHCPLease, error) {
	if raw == nil {
		return DHCPLease{}, fmt.Errorf("%w: missing lease", ErrInvalidDHCPLease)
	}
	lease, err := leaseFromACK(ifName, raw.ACK, cfg)
	if err != nil {
		return DHCPLease{}, err
	}
	lease.raw = raw
	return lease, nil
}

func leaseFromACK(ifName string, ack *dhcpv4.DHCPv4, cfg DHCPConfig) (DHCPLease, error) {
	if err := checkACKMessage(ack); err != nil {
		return DHCPLease{}, err
	}

	addr, prefix, err := addressFromACK(ack)
	if err != nil {
		return DHCPLease{}, err
	}

	router, err := routerFromACK(ack, cfg.RequireRouter)
	if err != nil {
		return DHCPLease{}, err
	}

	dns, err := dnsFromACK(ack, cfg.RequireDNS)
	if err != nil {
		return DHCPLease{}, err
	}

	leaseTime, t1, t2, err := timersFromACK(ack)
	if err != nil {
		return DHCPLease{}, err
	}

	if serverID, ok := parseIPv4(ack.ServerIdentifier()); !ok || !validUnicastIPv4(serverID) {
		return DHCPLease{}, fmt.Errorf("%w: missing or invalid server identifier", ErrInvalidDHCPLease)
	}

	mtu, err := mtuFromACK(ack)
	if err != nil {
		return DHCPLease{}, err
	}

	return DHCPLease{
		InterfaceName: ifName,
		Address:       addr,
		PrefixLength:  prefix,
		Router:        router,
		DNSServers:    dns,
		MTU:           mtu,
		LeaseTime:     leaseTime,
		T1:            t1,
		T2:            t2,
	}, nil
}

func checkACKMessage(ack *dhcpv4.DHCPv4) error {
	if ack == nil {
		return fmt.Errorf("%w: missing ACK", ErrInvalidDHCPLease)
	}
	if ack.MessageType() != dhcpv4.MessageTypeAck {
		return fmt.Errorf("%w: not an ACK message: %s", ErrInvalidDHCPLease, ack.MessageType())
	}
	return nil
}

func addressFromACK(ack *dhcpv4.DHCPv4) (netip.Addr, int, error) {
	addr, ok := parseIPv4(ack.YourIPAddr)
	if !ok || !validUnicastIPv4(addr) {
		return netip.Addr{}, 0, fmt.Errorf("%w: invalid client address %s", ErrInvalidDHCPLease, ack.YourIPAddr)
	}

	prefix, ok := prefixFromMask(ack.SubnetMask())
	if !ok {
		return netip.Addr{}, 0, fmt.Errorf("%w: missing or invalid subnet mask", ErrInvalidDHCPLease)
	}
	return addr, prefix, nil
}

func routerFromACK(ack *dhcpv4.DHCPv4, requireRouter bool) (netip.Addr, error) {
	router := netip.Addr{}
	routers := ack.Router()
	if len(routers) > 0 {
		var parsed bool
		router, parsed = parseIPv4(routers[0])
		if !parsed || !validUnicastIPv4(router) {
			return netip.Addr{}, fmt.Errorf("%w: invalid router %s", ErrInvalidDHCPLease, routers[0])
		}
	} else if requireRouter {
		return netip.Addr{}, fmt.Errorf("%w: missing router option", ErrInvalidDHCPLease)
	}
	return router, nil
}

func dnsFromACK(ack *dhcpv4.DHCPv4, requireDNS bool) ([]netip.Addr, error) {
	dns := make([]netip.Addr, 0, maxDNSServers)
	for _, server := range ack.DNS() {
		parsed, ok := parseIPv4(server)
		if !ok || !validUnicastIPv4(parsed) {
			return nil, fmt.Errorf("%w: invalid DNS %s", ErrInvalidDHCPLease, server)
		}
		dns = append(dns, parsed)
		if len(dns) == maxDNSServers {
			break
		}
	}
	if len(dns) == 0 && requireDNS {
		return nil, ErrDHCPDNSUnavailable
	}
	return dns, nil
}

func timersFromACK(ack *dhcpv4.DHCPv4) (leaseTime, t1, t2 time.Duration, err error) {
	leaseTime = ack.IPAddressLeaseTime(0)
	if leaseTime <= 0 {
		return 0, 0, 0, fmt.Errorf("%w: invalid lease time", ErrInvalidDHCPLease)
	}
	t1 = ack.IPAddressRenewalTime(leaseTime / 2)
	t2 = ack.IPAddressRebindingTime(leaseTime * 7 / 8)
	if t1 <= 0 || t1 >= t2 || t2 >= leaseTime {
		return 0, 0, 0, fmt.Errorf("%w: invalid T1/T2/LeaseTime", ErrInvalidDHCPLease)
	}
	return leaseTime, t1, t2, nil
}

func mtuFromACK(ack *dhcpv4.DHCPv4) (int, error) {
	raw := ack.GetOneOption(dhcpv4.OptionInterfaceMTU)
	if len(raw) == 0 {
		return 0, nil
	}
	if len(raw) != 2 {
		return 0, fmt.Errorf("%w: invalid MTU option length", ErrInvalidDHCPLease)
	}
	mtu := int(raw[0])<<8 | int(raw[1])
	if mtu < minDHCPMTU || mtu > maxDHCPMTU {
		return 0, fmt.Errorf("%w: invalid MTU %d", ErrInvalidDHCPLease, mtu)
	}
	return mtu, nil
}

func prefixFromMask(mask net.IPMask) (int, bool) {
	if len(mask) != net.IPv4len {
		return 0, false
	}
	ones, bits := mask.Size()
	return ones, bits == 32 && ones > 0 && ones <= 32
}

func parseIPv4(ip net.IP) (netip.Addr, bool) {
	v4 := ip.To4()
	if v4 == nil {
		return netip.Addr{}, false
	}
	addr, ok := netip.AddrFromSlice(v4)
	return addr, ok
}

func validUnicastIPv4(addr netip.Addr) bool {
	return addr.Is4() && !addr.IsUnspecified() && !addr.IsLoopback() && !addr.IsMulticast()
}

func dhcpTimeout(cfg DHCPConfig) time.Duration {
	if cfg.Timeout > 0 {
		return cfg.Timeout
	}
	return 30 * time.Second
}

func dhcpRetries(cfg DHCPConfig) int {
	if cfg.Retries > 0 {
		return cfg.Retries
	}
	return 3
}
