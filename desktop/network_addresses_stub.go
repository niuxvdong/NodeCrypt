//go:build !windows

package main

func platformLocalIPv4Addresses() ([]string, bool) {
	return nil, false
}
