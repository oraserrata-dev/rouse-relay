//go:build windows

// © 2026 Ora Serrata LLC. All rights reserved.

package main

import "syscall"

// setBroadcastOpt enables SO_BROADCAST on the raw socket fd so a UDP magic
// packet can be sent to a broadcast address. Windows variant: the fd is a
// syscall.Handle rather than a plain int.
func setBroadcastOpt(fd uintptr) error {
	return syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
}
