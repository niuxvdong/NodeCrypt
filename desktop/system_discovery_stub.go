//go:build !windows

package main

type systemDiscovery interface {
	Close()
	SetName(string)
}

func startSystemDiscovery(string, string, int, func(NodeInfo)) systemDiscovery { return nil }
