//go:build windows

package main

import "testing"

func TestPreferredAdapterAddressesUseWiFiAndLowestMetric(t *testing.T) {
	candidates := []adapterAddressCandidate{
		{address: "10.0.0.5", index: 8, metric: 5},
		{address: "192.168.10.20", index: 12, metric: 35, wifi: true},
		{address: "192.168.3.115", index: 4, metric: 10, wifi: true},
	}
	addresses := selectPreferredAdapterAddresses(candidates)
	if len(addresses) != 1 || addresses[0] != "192.168.3.115" {
		t.Fatalf("unexpected preferred addresses: %#v", addresses)
	}
}

func TestVirtualAdapterNamesAreFiltered(t *testing.T) {
	for _, name := range []string{"vEthernet (Default Switch)", "VMware Network Adapter", "本地连接* 12", "Tailscale Tunnel"} {
		if !isVirtualAdapterName(name) {
			t.Fatalf("virtual adapter was not filtered: %s", name)
		}
	}
	if isVirtualAdapterName("Intel(R) Wi-Fi 6 AX201 160MHz") {
		t.Fatal("physical Wi-Fi adapter was incorrectly filtered")
	}
}
