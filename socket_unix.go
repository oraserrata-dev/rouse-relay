//go:build !windows

// © 2026 Ora Serrata LLC. All rights reserved.

package main

import "syscall"

// setBroadcastOpt enables SO_BROADCAST on the raw socket fd so a UDP magic
// packet can be sent to a broadcast address. Unix variant: the fd is a plain
// int. Without this, sending to 255.255.255.255 fails with EACCES on Linux.
func setBroadcastOpt(fd uintptr) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
}
