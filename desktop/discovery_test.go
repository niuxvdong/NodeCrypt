package main

import (
	"testing"
	"time"
)

func TestDiscoveryServicesFindEachOther(t *testing.T) {
	first := StartDiscovery("test-node-first", "First node", 41001)
	second := StartDiscovery("test-node-second", "Second node", 41002)
	if first == nil || second == nil {
		t.Fatal("multicast discovery could not start")
	}
	if first.reader == nil || first.writer == nil || second.reader == nil || second.writer == nil {
		t.Fatalf("multicast sockets unavailable: first reader=%v writer=%v, second reader=%v writer=%v", first.reader != nil, first.writer != nil, second.reader != nil, second.writer != nil)
	}
	defer first.Close()
	defer second.Close()

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		first.Announce()
		second.Announce()
		if containsNode(first.Nodes(), "test-node-second") && containsNode(second.Nodes(), "test-node-first") {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("nodes did not discover each other: first=%#v second=%#v systemFirst=%v systemSecond=%v", first.Nodes(), second.Nodes(), first.system != nil, second.system != nil)
}

func TestUDPAddressRemainsPreferredOverSystemAddress(t *testing.T) {
	discovery := &DiscoveryService{id: "local", nodes: make(map[string]discoveredNode)}
	discovery.rememberNode(NodeInfo{ID: "peer", Name: "Peer", Address: "10.10.0.8", Port: 8788}, false)
	discovery.rememberNode(NodeInfo{ID: "peer", Name: "Peer", Address: "192.168.3.22", Port: 8788}, true)
	discovery.rememberNode(NodeInfo{ID: "peer", Name: "Peer", Address: "10.10.0.8", Port: 8788}, false)

	node := discovery.nodes["peer"].info
	if node.Address != "192.168.3.22" || node.URL != "http://192.168.3.22:8788" {
		t.Fatalf("UDP source address was not preferred: %#v", node)
	}
	if len(node.Addresses) != 2 || node.Addresses[1] != "10.10.0.8" {
		t.Fatalf("candidate addresses were not merged: %#v", node.Addresses)
	}
}

func containsNode(nodes []NodeInfo, id string) bool {
	for _, node := range nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}
