//go:build windows

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const enableEchoInput = 0x0004

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

var stdinReader = bufio.NewReader(os.Stdin)

func readSecretLine(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	fd := os.Stdin.Fd()
	var mode uint32
	isConsole := false
	if r1, _, _ := procGetConsoleMode.Call(fd, uintptr(unsafe.Pointer(&mode))); r1 != 0 {
		isConsole = true
		_, _, _ = procSetConsoleMode.Call(fd, uintptr(mode&^enableEchoInput))
		defer procSetConsoleMode.Call(fd, uintptr(mode))
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if isConsole {
		fmt.Fprintln(os.Stderr)
	}
	if err != nil && len(line) == 0 {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
