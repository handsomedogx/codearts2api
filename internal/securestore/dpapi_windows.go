//go:build windows

package securestore

import (
	"fmt"
	"syscall"
	"unsafe"
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

const cryptProtectUIForbidden = 0x1

var (
	crypt32                = syscall.NewLazyDLL("crypt32.dll")
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	procLocalFree          = kernel32.NewProc("LocalFree")
)

func available() bool                { return true }
func platformPrefix() string         { return "dpapi:v1:" }
func platformProtectionName() string { return "dpapi-current-user" }
func prepareRuntime(...string) error { return nil }

func protectBytes(plain []byte) ([]byte, error) {
	in := bytesToBlob(plain)
	var out dataBlob
	r1, _, callErr := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		0,
		0,
		0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 == 0 {
		return nil, fmt.Errorf("CryptProtectData failed: %w", callErr)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return blobBytes(out), nil
}

func unprotectBytes(cipher []byte) ([]byte, error) {
	in := bytesToBlob(cipher)
	var out dataBlob
	r1, _, callErr := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		0,
		0,
		0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r1 == 0 {
		return nil, fmt.Errorf("CryptUnprotectData failed: %w", callErr)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return blobBytes(out), nil
}

func bytesToBlob(value []byte) dataBlob {
	if len(value) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(value)), pbData: &value[0]}
}

func blobBytes(blob dataBlob) []byte {
	if blob.cbData == 0 || blob.pbData == nil {
		return []byte{}
	}
	view := unsafe.Slice(blob.pbData, int(blob.cbData))
	result := make([]byte, len(view))
	copy(result, view)
	return result
}
