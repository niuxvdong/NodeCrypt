//go:build windows

package main

import (
	"testing"
	"time"
)

func TestNativeDNSDiscoveryFindsPeer(t *testing.T) {
	firstNodes := make(chan NodeInfo, 8)
	secondNodes := make(chan NodeInfo, 8)
	first := startSystemDiscovery("dns-test-first", "DNS first", 42001, func(node NodeInfo) {
		firstNodes <- node
	})
	second := startSystemDiscovery("dns-test-second", "DNS second", 42002, func(node NodeInfo) {
		secondNodes <- node
	})
	if first == nil || second == nil {
		t.Skip("Windows DNS-SD is unavailable")
	}
	defer first.Close()
	defer second.Close()

	deadline := time.After(8 * time.Second)
	foundFirst := false
	foundSecond := false
	for !foundFirst || !foundSecond {
		select {
		case node := <-firstNodes:
			foundFirst = node.ID == "dns-test-second" && node.Port == 42002 && node.Address != ""
		case node := <-secondNodes:
			foundSecond = node.ID == "dns-test-first" && node.Port == 42001 && node.Address != ""
		case <-deadline:
			t.Fatalf("DNS-SD peers were not both resolved: first=%v second=%v", foundFirst, foundSecond)
		}
	}
}
