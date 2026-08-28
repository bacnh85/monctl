//go:build windows

package main

import "golang.design/x/hotkey"

// hkModAlt normalizes the Alt modifier name across platforms.
const hkModAlt = hotkey.ModAlt

// registerHint is empty on Windows (no extra permission needed for hotkeys).
const registerHint = ""
