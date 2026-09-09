//go:build !windows

package main

import (
	"fmt"
	"os"
)

func cmdProbeInput(argv []string) int {
	fmt.Fprintln(os.Stderr, "monctl: probe-input is only implemented on Windows")
	return 1
}
