//go:build linux

package system

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

var (
	ErrNetworkInterfaceNotFound  = errors.New("network interface not found")
	ErrNetworkInterfaceAmbiguous = errors.New("multiple candidate network interfaces found")
)

type linkConfigurator interface {
	LinkByName(name string) (netlink.Link, error)
	LinkList() ([]netlink.Link, error)
	LinkSetMTU(link netlink.Link, mtu int) error
	AddrReplace(link netlink.Link, addr *netlink.Addr) error
	RouteReplace(route *netlink.Route) error
	AddrDel(link netlink.Link, addr *netlink.Addr) error
	RouteDel(route *netlink.Route) error
}

type netlinkConfigurator struct{}

func (netlinkConfigurator) LinkByName(name string) (netlink.Link, error) {
	return netlink.LinkByName(name)
}
func (netlinkConfigurator) LinkList() ([]netlink.Link, error) { return netlink.LinkList() }
func (netlinkConfigurator) LinkSetUp(link netlink.Link) error { return netlink.LinkSetUp(link) }
func (netlinkConfigurator) LinkSetMTU(link netlink.Link, mtu int) error {
	return netlink.LinkSetMTU(link, mtu)
}

func (netlinkConfigurator) AddrReplace(link netlink.Link, addr *netlink.Addr) error {
	return netlink.AddrReplace(link, addr)
}

func (netlinkConfigurator) RouteReplace(route *netlink.Route) error {
	return netlink.RouteReplace(route)
}

func (netlinkConfigurator) AddrDel(link netlink.Link, addr *netlink.Addr) error {
	return netlink.AddrDel(link, addr)
}
func (netlinkConfigurator) RouteDel(route *netlink.Route) error { return netlink.RouteDel(route) }

func configureLoopback(name string) error {
	if name == "" {
		name = "lo"
	}
	lo, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	if err := netlink.LinkSetUp(lo); err != nil {
		return err
	}
	slog.Info("loopback interface is up", "component", "system", "interface", name)
	return nil
}

func configureDHCPv4Network(ctx context.Context, cfg Config) error {
	if !cfg.DHCP.Enabled {
		return ErrDHCPDisabled
	}
	links := netlinkConfigurator{}
	link, err := selectEthernetLink(links, cfg.EthernetName)
	if err != nil {
		return err
	}

	if err := links.LinkSetUp(link); err != nil {
		return fmt.Errorf("bring up interface %s: %w", link.Attrs().Name, err)
	}

	client, err := newInsomniacDHCPClient(link.Attrs().Name, cfg.DHCP)
	if err != nil {
		return err
	}
	lease, err := client.Acquire(ctx, link.Attrs().Name, cfg.DHCP)
	if err != nil {
		_ = client.Close()
		return err
	}
	if err := applyDHCPLease(links, link, cfg, lease); err != nil {
		_ = client.Close()
		return err
	}
	startLeaseManager(client, links, link, cfg, lease)
	slog.Info("DHCPv4 lease applied", "component", "system", "interface", link.Attrs().Name, "address", lease.Address.String(), "prefix_length", lease.PrefixLength, "router", lease.Router.String(), "dns_count", len(lease.DNSServers), "lease", lease.LeaseTime.String())
	return nil
}

func selectEthernetLink(links linkConfigurator, name string) (netlink.Link, error) {
	if name != "" {
		link, err := links.LinkByName(name)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrNetworkInterfaceNotFound, name, err)
		}
		if isLoopback(link) {
			return nil, fmt.Errorf("%w: %s is loopback", ErrNetworkInterfaceNotFound, name)
		}
		return link, nil
	}

	all, err := links.LinkList()
	if err != nil {
		return nil, fmt.Errorf("enumerate network interfaces: %w", err)
	}
	var candidates []netlink.Link
	for _, link := range all {
		if !isLoopback(link) {
			candidates = append(candidates, link)
		}
	}
	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		return nil, ErrNetworkInterfaceNotFound
	default:
		return nil, fmt.Errorf("%w: %d candidate interfaces", ErrNetworkInterfaceAmbiguous, len(candidates))
	}
}

func isLoopback(link netlink.Link) bool {
	attrs := link.Attrs()
	return attrs != nil && attrs.Flags&net.FlagLoopback != 0
}

func applyDHCPLease(links linkConfigurator, link netlink.Link, cfg Config, lease DHCPLease) error {
	if lease.MTU > 0 {
		if err := links.LinkSetMTU(link, lease.MTU); err != nil {
			return fmt.Errorf("apply DHCP MTU: %w", err)
		}
	}
	addr, err := netlinkAddr(lease)
	if err != nil {
		return err
	}
	if err := links.AddrReplace(link, addr); err != nil {
		return fmt.Errorf("apply DHCP address: %w", err)
	}
	if lease.Router.IsValid() {
		if err := links.RouteReplace(defaultRoute(link, lease)); err != nil {
			return fmt.Errorf("apply DHCP default route: %w", err)
		}
	}
	if err := writeResolvConf(cfg.ResolvConfPath, lease.DNSServers); err != nil {
		return fmt.Errorf("write DHCP DNS: %w", err)
	}
	return nil
}

func netlinkAddr(lease DHCPLease) (*netlink.Addr, error) {
	ip := net.IP(lease.Address.AsSlice())
	mask := net.CIDRMask(lease.PrefixLength, 32)
	if mask == nil {
		return nil, fmt.Errorf("%w: invalid prefix %d", ErrInvalidDHCPLease, lease.PrefixLength)
	}
	return &netlink.Addr{IPNet: &net.IPNet{IP: ip, Mask: mask}}, nil
}

func defaultRoute(link netlink.Link, lease DHCPLease) *netlink.Route {
	_, dst, _ := net.ParseCIDR("0.0.0.0/0")
	return &netlink.Route{
		Scope:     unix.RT_SCOPE_UNIVERSE,
		LinkIndex: link.Attrs().Index,
		Dst:       dst,
		Gw:        net.IP(lease.Router.AsSlice()),
	}
}

func startLeaseManager(client dhcpClient, links linkConfigurator, link netlink.Link, cfg Config, initial DHCPLease) {
	go func() {
		lease := initial
		defer func() { _ = client.Close() }()
		for {
			time.Sleep(lease.T1)

			if renewed, ok := renewLease(client, links, link, cfg, lease); ok {
				lease = renewed
				continue
			}
			if waitT2 := lease.T2 - lease.T1; waitT2 > 0 {
				time.Sleep(waitT2)
			}
			if rebound, ok := rebindLease(client, links, link, cfg, lease); ok {
				lease = rebound
				continue
			}
			if remaining := lease.LeaseTime - lease.T2; remaining > 0 {
				time.Sleep(remaining)
			}
			revokeDHCPLease(links, link, lease)
			slog.Error("DHCPv4 lease expired and could not be recovered; VeriRelay fails closed", "component", "system")
			os.Exit(1)
		}
	}()
}

func renewLease(client dhcpClient, links linkConfigurator, link netlink.Link, cfg Config, lease DHCPLease) (DHCPLease, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), dhcpTimeout(cfg.DHCP))
	renewed, err := client.Renew(ctx, lease, cfg.DHCP)
	cancel()
	if err != nil {
		slog.Warn("DHCPv4 T1 renewal failed", "component", "system", "err", err)
		return DHCPLease{}, false
	}
	if err := applyDHCPLease(links, link, cfg, renewed); err != nil {
		slog.Warn("DHCPv4 renewal lease apply failed", "component", "system", "err", err)
		return DHCPLease{}, false
	}
	slog.Info("DHCPv4 lease renewed", "component", "system", "interface", link.Attrs().Name, "address", renewed.Address.String(), "prefix_length", renewed.PrefixLength, "lease", renewed.LeaseTime.String())
	return renewed, true
}

func rebindLease(client dhcpClient, links linkConfigurator, link netlink.Link, cfg Config, lease DHCPLease) (DHCPLease, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), dhcpTimeout(cfg.DHCP))
	rebound, err := client.Rebind(ctx, lease, cfg.DHCP)
	cancel()
	if err != nil {
		slog.Warn("DHCPv4 T2 rebind failed", "component", "system", "err", err)
		return DHCPLease{}, false
	}
	if err := applyDHCPLease(links, link, cfg, rebound); err != nil {
		slog.Warn("DHCPv4 rebind lease apply failed", "component", "system", "err", err)
		return DHCPLease{}, false
	}
	slog.Info("DHCPv4 lease rebound", "component", "system", "interface", link.Attrs().Name, "address", rebound.Address.String(), "prefix_length", rebound.PrefixLength, "lease", rebound.LeaseTime.String())
	return rebound, true
}

func revokeDHCPLease(links linkConfigurator, link netlink.Link, lease DHCPLease) {
	addr, err := netlinkAddr(lease)
	if err == nil {
		_ = links.AddrDel(link, addr)
	}
	if lease.Router.IsValid() {
		_ = links.RouteDel(defaultRoute(link, lease))
	}
}
