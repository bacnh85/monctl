//go:build !windows && !darwin

package main

import "github.com/bacnh85/monctl/monitor"

// No native brightness-key interception on linux: brightness keys are
// vendor-specific; use config hotkeys. (Windows has the raw-input watcher
// in hotkey_media_windows.go; macOS has extraHotkeys in hotkey_media_darwin.go.)

func extraHotkeys() []nativeKeyBinding { return nil }

func watchNativeBrightness(apply func(hk monitor.Hotkey) error) {}
