//go:build linux

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

var stdinReader = bufio.NewReader(os.Stdin)

func readSecretLine(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := os.Stdin.Fd()
	var old syscall.Termios
	isTTY := false
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&old))); errno == 0 {
		isTTY = true
		updated := old
		updated.Lflag &^= syscall.ECHO
		_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(&updated)))
		defer syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(&old)))
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if isTTY {
		fmt.Fprintln(os.Stderr)
	}
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
