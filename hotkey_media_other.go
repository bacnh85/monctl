//go:build !windows

package main

import "github.com/bacnh85/monctl/monitor"

// No native brightness-key interception on darwin/linux: macOS catches F1/F2
// via extraHotkeys (media events); Linux brightness keys are vendor-specific.
// The Windows raw-input watcher is a no-op here.

func extraHotkeys() []nativeKeyBinding { return nil }

func watchNativeBrightness(apply func(hk monitor.Hotkey) error) {}
