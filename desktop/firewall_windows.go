//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	firewallTCPRuleName = "NodeCrypt Desktop TCP (Local Subnet)"
	firewallUDPRuleName = "NodeCrypt Desktop Discovery (Local Subnet)"
)

type NetworkStatus struct {
	NetworkName            string `json:"networkName"`
	NetworkCategory        string `json:"networkCategory"`
	FirewallConfigured     bool   `json:"firewallConfigured"`
	FirewallTCPConfigured  bool   `json:"firewallTcpConfigured"`
	FirewallUDPConfigured  bool   `json:"firewallUdpConfigured"`
	FirewallProgram        string `json:"firewallProgram"`
	SystemDiscoveryEnabled bool   `json:"systemDiscoveryEnabled"`
}

func handleMaintenanceCommand(arguments []string) bool {
	if len(arguments) < 2 {
		return false
	}
	var err error
	switch arguments[1] {
	case "--configure-firewall":
		err = installFirewallRules()
	case "--remove-firewall":
		err = removeFirewallRules()
	default:
		return false
	}
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
	return true
}

func installFirewallRules() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	_ = removeFirewallRules()
	for _, arguments := range firewallRuleCommands(executable) {
		if output, err := exec.Command("netsh.exe", arguments...).CombinedOutput(); err != nil {
			return fmt.Errorf("netsh: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func firewallRuleCommands(executable string) [][]string {
	return [][]string{
		{"advfirewall", "firewall", "add", "rule", "name=" + firewallTCPRuleName, "dir=in", "action=allow", "program=" + executable, "enable=yes", "profile=any", "protocol=TCP", "localport=8788-8807", "remoteip=LocalSubnet", "edge=no"},
		{"advfirewall", "firewall", "add", "rule", "name=" + firewallUDPRuleName, "dir=in", "action=allow", "program=" + executable, "enable=yes", "profile=any", "protocol=UDP", "localport=42429", "remoteip=LocalSubnet", "edge=no"},
	}
}

func removeFirewallRules() error {
	for _, name := range []string{firewallTCPRuleName, firewallUDPRuleName} {
		_ = exec.Command("netsh.exe", "advfirewall", "firewall", "delete", "rule", "name="+name).Run()
	}
	return nil
}

func requestFirewallConfiguration() bool {
	return requestElevatedMaintenance("--configure-firewall")
}

func requestFirewallRemoval() bool {
	return requestElevatedMaintenance("--remove-firewall")
}

func requestElevatedMaintenance(argument string) bool {
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return false
	}
	quotedExecutable := strings.ReplaceAll(executable, "'", "''")
	script := fmt.Sprintf(`$ErrorActionPreference='Stop'; $p=Start-Process -FilePath '%s' -ArgumentList '%s' -Verb RunAs -WindowStyle Hidden -Wait -PassThru; exit $p.ExitCode`, quotedExecutable, argument)
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	return command.Run() == nil
}

func queryWindowsNetworkStatus(primaryIP string) NetworkStatus {
	executable, _ := os.Executable()
	executable, _ = filepath.Abs(executable)
	status := NetworkStatus{FirewallProgram: executable}
	if primaryIP == "" {
		return status
	}
	primaryIP = strings.ReplaceAll(primaryIP, "'", "''")
	quotedExecutable := strings.ReplaceAll(executable, "'", "''")
	script := fmt.Sprintf(`$ip=Get-NetIPAddress -IPAddress '%s' -ErrorAction SilentlyContinue | Select-Object -First 1; $profile=if($ip){Get-NetConnectionProfile -InterfaceIndex $ip.InterfaceIndex -ErrorAction SilentlyContinue | Select-Object -First 1}; $exe='%s'; $tcp=Get-NetFirewallRule -DisplayName '%s' -ErrorAction SilentlyContinue | Where-Object {$_.Enabled -eq 'True' -and $_.Action -eq 'Allow' -and (($_ | Get-NetFirewallApplicationFilter).Program -ieq $exe)}; $udp=Get-NetFirewallRule -DisplayName '%s' -ErrorAction SilentlyContinue | Where-Object {$_.Enabled -eq 'True' -and $_.Action -eq 'Allow' -and (($_ | Get-NetFirewallApplicationFilter).Program -ieq $exe)}; [pscustomobject]@{networkName=if($profile){$profile.Name}else{''};networkCategory=if($profile){[string]$profile.NetworkCategory}else{'Unknown'};firewallConfigured=[bool]($tcp -and $udp);firewallTcpConfigured=[bool]$tcp;firewallUdpConfigured=[bool]$udp;firewallProgram=$exe} | ConvertTo-Json -Compress`, primaryIP, quotedExecutable, firewallTCPRuleName, firewallUDPRuleName)
	output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script).Output()
	if err == nil {
		_ = json.Unmarshal(output, &status)
	}
	return status
}
