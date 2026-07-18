//go:build !windows

package main

type NetworkStatus struct {
	NetworkName            string `json:"networkName"`
	NetworkCategory        string `json:"networkCategory"`
	FirewallConfigured     bool   `json:"firewallConfigured"`
	FirewallTCPConfigured  bool   `json:"firewallTcpConfigured"`
	FirewallUDPConfigured  bool   `json:"firewallUdpConfigured"`
	FirewallProgram        string `json:"firewallProgram"`
	SystemDiscoveryEnabled bool   `json:"systemDiscoveryEnabled"`
}

func handleMaintenanceCommand([]string) bool         { return false }
func requestFirewallConfiguration() bool             { return false }
func requestFirewallRemoval() bool                   { return false }
func queryWindowsNetworkStatus(string) NetworkStatus { return NetworkStatus{} }
