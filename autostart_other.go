//go:build !windows

package main

import (
	"fmt"
	"os"
)

func cmdAutostart(argv []string) int {
	fmt.Fprintln(os.Stderr, "monctl: autostart is not implemented on this platform yet")
	return 1
}
