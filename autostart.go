//go:build windows

// config + autostart subcommands (HKCU Run key registration).
package main

import (
	"flag"
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const runValueName = "monctl"

func cmdAutostart(argv []string) int {
	remove := false
	fs := flag.NewFlagSet("autostart", flag.ContinueOnError)
	fs.BoolVar(&remove, "remove", false, "remove autostart entry")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return fail(err)
	}
	defer key.Close()

	if remove {
		if err := key.DeleteValue(runValueName); err != nil {
			return fail(err)
		}
		fmt.Println("autostart removed")
		return 0
	}
	exe, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	val := fmt.Sprintf(`"%s" watch`, exe)
	if err := key.SetStringValue(runValueName, val); err != nil {
		return fail(err)
	}
	fmt.Printf("autostart registered: %s\n", val)
	return 0
}
