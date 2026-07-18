//go:build windows

package main

import (
	"encoding/base64"
	"unsafe"

	"golang.org/x/sys/windows"
)

const cryptProtectUIForbidden = 0x1

var recentRoomEntropy = []byte("NodeCrypt Desktop recent room v1")

func protectRecentRoomSecret(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	inputBytes := []byte(value)
	input := dataBlob(inputBytes)
	entropy := dataBlob(recentRoomEntropy)
	var output windows.DataBlob
	if err := windows.CryptProtectData(&input, nil, &entropy, 0, nil, cryptProtectUIForbidden, &output); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	protected := append([]byte(nil), unsafe.Slice(output.Data, output.Size)...)
	return base64.StdEncoding.EncodeToString(protected), nil
}

func unprotectRecentRoomSecret(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	protected, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	input := dataBlob(protected)
	entropy := dataBlob(recentRoomEntropy)
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(&input, nil, &entropy, 0, nil, cryptProtectUIForbidden, &output); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(output.Data)))
	plaintext := append([]byte(nil), unsafe.Slice(output.Data, output.Size)...)
	return string(plaintext), nil
}

func dataBlob(value []byte) windows.DataBlob {
	if len(value) == 0 {
		return windows.DataBlob{}
	}
	return windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
}
