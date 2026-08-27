//go:build darwin

package main

import "golang.design/x/hotkey"

// hkModAlt: the darwin backend names Alt "Option".
const hkModAlt = hotkey.ModOption
