//go:build linux

package main

import "golang.design/x/hotkey"

// hkModAlt: the X11 backend names Alt "Mod1" (no ModAlt constant).
const hkModAlt = hotkey.Mod1
