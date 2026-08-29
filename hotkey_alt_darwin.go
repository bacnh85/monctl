//go:build darwin

package main

import "golang.design/x/hotkey"

// hkModAlt: the darwin backend names Alt "Option".
const hkModAlt = hotkey.ModOption

// hkModCmd: Cmd/Win alias (Windows key on PC keyboards = Cmd on macOS).
const hkModCmd = hotkey.ModCmd
