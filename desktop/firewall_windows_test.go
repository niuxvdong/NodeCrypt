//go:build windows

package main

import (
	"strings"
	"testing"
)

func TestFirewallRulesAreRestrictedToProgramAndLocalSubnet(t *testing.T) {
	executable := `C:\Program Files\NodeCrypt\NodeCrypt-Desktop.exe`
	commands := firewallRuleCommands(executable)
	if len(commands) != 2 {
		t.Fatalf("expected two firewall commands, got %d", len(commands))
	}
	tcp := strings.Join(commands[0], " ")
	udp := strings.Join(commands[1], " ")
	for _, expected := range []string{"program=" + executable, "remoteip=LocalSubnet", "profile=any", "dir=in", "action=allow"} {
		if !strings.Contains(tcp, expected) || !strings.Contains(udp, expected) {
			t.Fatalf("firewall commands are missing %q: TCP=%s UDP=%s", expected, tcp, udp)
		}
	}
	if !strings.Contains(tcp, "protocol=TCP") || !strings.Contains(tcp, "localport=8788-8807") {
		t.Fatalf("unexpected TCP rule: %s", tcp)
	}
	if !strings.Contains(udp, "protocol=UDP") || !strings.Contains(udp, "localport=42429") {
		t.Fatalf("unexpected UDP rule: %s", udp)
	}
}
