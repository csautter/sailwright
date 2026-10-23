//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func sessionInfo() string {
	sid, _ := unix.Getsid(0)
	tty := "no-tty"
	if f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		_ = f.Close()
		tty = "has-tty"
	}
	return fmt.Sprintf("pid=%d sid=%d pgid=%d %s", os.Getpid(), sid, syscall.Getpgrp(), tty)
}
