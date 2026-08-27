//go:build windows

package main

import "golang.design/x/hotkey"

// hkModAlt normalizes the Alt modifier name across platforms.
const hkModAlt = hotkey.ModAlt
