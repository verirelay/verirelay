package system

import "time"

// Config describes the system initialization steps performed during the
// PID 1 startup phase.
type Config struct {
	ProcPath       string
	DevPath        string
	ResolvConfPath string
	LoopbackName   string
	EthernetName   string
	DHCP           DHCPConfig
}

// DHCPConfig describes the minimal DHCPv4 network initialization policy.
type DHCPConfig struct {
	Enabled       bool
	Timeout       time.Duration
	Retries       int
	RequireRouter bool
	RequireDNS    bool
}

// DefaultConfig returns the default system initialization parameters used by
// the current image environment.
func DefaultConfig() Config {
	return Config{
		ProcPath:       "/proc",
		DevPath:        "/dev",
		ResolvConfPath: "/etc/resolv.conf",
		LoopbackName:   "lo",
		EthernetName:   "eth0",
		DHCP: DHCPConfig{
			Enabled:       true,
			Timeout:       30 * time.Second,
			Retries:       3,
			RequireRouter: true,
			RequireDNS:    true,
		},
	}
}
