//go:build !windows

package main

import "errors"

func protectRecentRoomSecret(string) (string, error) {
	return "", errors.New("recent room secrets require Windows DPAPI")
}

func unprotectRecentRoomSecret(string) (string, error) {
	return "", errors.New("recent room secrets require Windows DPAPI")
}
