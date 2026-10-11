//go:build !windows && !linux

package securestore

import "fmt"

func available() bool                { return false }
func platformPrefix() string         { return "unsupported:v1:" }
func platformProtectionName() string { return "unsupported" }
func prepareRuntime(...string) error { return nil }

func protectBytes([]byte) ([]byte, error) {
	return nil, fmt.Errorf("DPAPI is available only on Windows")
}

func unprotectBytes([]byte) ([]byte, error) {
	return nil, fmt.Errorf("DPAPI is available only on Windows")
}
