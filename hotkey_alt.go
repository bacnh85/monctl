//go:build windows || darwin

package main

import "golang.design/x/hotkey"

// hkModAlt normalizes the Alt/Option modifier name across platforms.
const hkModAlt = hotkey.ModAlt
