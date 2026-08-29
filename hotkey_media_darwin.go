//go:build darwin

package main

import (
	"github.com/bacnh85/monctl/monitor"
	"golang.design/x/hotkey"
)

// watchNativeBrightness is a no-op on macOS: native brightness arrives via
// extraHotkeys above; the raw-input watcher is Windows-only.
func watchNativeBrightness(apply func(hk monitor.Hotkey) error) {}

// extraHotkeys declares the macOS native brightness keys — MEDIA EVENTS
// only (NX_KEYTYPE_* from <IOKit/hidsystem/ev_keymap.h>). Plain F1/F2
// keycodes are deliberately NOT tapped: bare F1/F2 must keep working as
// normal function keys for apps. With System Settings -> Keyboard ->
// "Use F1, F2 as standard function keys" enabled, Fn+F1/Fn+F2 emit the
// brightness media events caught here; bare F1/F2 reach apps untouched.
func extraHotkeys() []nativeKeyBinding {
	return []nativeKeyBinding{
		{name: "brightness-down", action: "brightness", value: "-10", cands: []hotkey.Key{
			hotkey.Key(1<<16 | 3), // NX_KEYTYPE_BRIGHTNESS_DOWN (ev_keymap.h)
			hotkey.Key(0x91),      // vendor keycode captured from Logitech Fn layer (BD-style)
		}},
		{name: "brightness-up", action: "brightness", value: "+10", cands: []hotkey.Key{
			hotkey.Key(1<<16 | 2), // NX_KEYTYPE_BRIGHTNESS_UP (ev_keymap.h)
			hotkey.Key(0x90),      // vendor keycode captured from Logitech Fn layer (BD-style)
		}},
	}
}
