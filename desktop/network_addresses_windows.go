//go:build windows

package main

import (
	"net"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

type adapterAddressCandidate struct {
	address string
	index   uint32
	metric  uint32
	wifi    bool
}

func platformLocalIPv4Addresses() ([]string, bool) {
	size := uint32(15 * 1024)
	for attempts := 0; attempts < 3; attempts++ {
		buffer := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buffer[0]))
		err := windows.GetAdaptersAddresses(
			uint32(windows.AF_INET),
			windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER,
			0,
			first,
			&size,
		)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return nil, true
		}
		candidates := make([]adapterAddressCandidate, 0)
		for adapter := first; adapter != nil; adapter = adapter.Next {
			if adapter.OperStatus != windows.IfOperStatusUp || adapter.PhysicalAddressLength == 0 {
				continue
			}
			wifi := adapter.IfType == windows.IF_TYPE_IEEE80211
			if !wifi && adapter.IfType != windows.IF_TYPE_ETHERNET_CSMACD {
				continue
			}
			name := windows.UTF16PtrToString(adapter.FriendlyName)
			description := windows.UTF16PtrToString(adapter.Description)
			if isVirtualAdapterName(name + " " + description) {
				continue
			}
			for unicast := adapter.FirstUnicastAddress; unicast != nil; unicast = unicast.Next {
				if unicast.DadState != windows.IpDadStatePreferred {
					continue
				}
				ip := unicast.Address.IP()
				if !isUsableLocalIPv4(ip) {
					continue
				}
				candidates = append(candidates, adapterAddressCandidate{
					address: net.IP(ip).To4().String(),
					index:   adapter.IfIndex,
					metric:  adapter.Ipv4Metric,
					wifi:    wifi,
				})
			}
		}
		return selectPreferredAdapterAddresses(candidates), true
	}
	return nil, true
}

func selectPreferredAdapterAddresses(candidates []adapterAddressCandidate) []string {
	hasWiFi := false
	for _, candidate := range candidates {
		if candidate.wifi {
			hasWiFi = true
			break
		}
	}
	filtered := make([]adapterAddressCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.wifi == hasWiFi {
			filtered = append(filtered, candidate)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filtered[i].metric != filtered[j].metric {
			return filtered[i].metric < filtered[j].metric
		}
		if filtered[i].index != filtered[j].index {
			return filtered[i].index < filtered[j].index
		}
		return filtered[i].address < filtered[j].address
	})
	if len(filtered) == 0 {
		return nil
	}
	selectedIndex := filtered[0].index
	addresses := make([]string, 0, 1)
	seen := make(map[string]bool)
	for _, candidate := range filtered {
		if candidate.index == selectedIndex && !seen[candidate.address] {
			seen[candidate.address] = true
			addresses = append(addresses, candidate.address)
		}
	}
	return addresses
}

func isVirtualAdapterName(value string) bool {
	value = strings.ToLower(value)
	for _, marker := range []string{
		"virtual", "hyper-v", "vmware", "virtualbox", "vethernet", "wsl", "docker",
		"vpn", "wireguard", "tailscale", "zerotier", " tap", " tun", "loopback",
		"bluetooth", "wi-fi direct", "mobile hotspot", "虚拟", "蓝牙", "本地连接*",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
