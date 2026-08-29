//go:build !darwin

package main

import "golang.design/x/hotkey"

// hkModAlt: the X11 backend names Alt "Mod1" (no ModAlt constant).
const hkModAlt = hotkey.Mod1

// hkModCmd: X11 has no Win modifier name — Super is Mod4.
const hkModCmd = hotkey.Mod4
