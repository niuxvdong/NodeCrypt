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

func containsNode(nodes []NodeInfo, id string) bool {
	for _, node := range nodes {
		if node.ID == id {
			return true
		}
	}
	return false
}
