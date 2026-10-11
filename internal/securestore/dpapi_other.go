//go:build !windows

package securestore

import "fmt"

func available() bool { return false }

func protectBytes([]byte) ([]byte, error) {
	return nil, fmt.Errorf("DPAPI is available only on Windows")
}

func unprotectBytes([]byte) ([]byte, error) {
	return nil, fmt.Errorf("DPAPI is available only on Windows")
}
