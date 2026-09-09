//go:build !windows

package main

import "fmt"

func cmdProbeInput(argv []string) int {
	fmt.Fprintln(stderr, "monctl: probe-input is only implemented on Windows")
	return 1
}
