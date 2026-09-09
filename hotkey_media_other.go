//go:build !windows && !darwin

package main

import "github.com/bacnh85/monctl/monitor"

// No native brightness-key interception off macOS/Windows (Linux brightness
// keys are vendor-specific; use config hotkeys).

func extraHotkeys() []nativeKeyBinding { return nil }

func watchNativeBrightness(apply func(hk monitor.Hotkey) error) {}
