//go:build darwin

package main

import "golang.design/x/hotkey"

// extraHotkeys declares the macOS native brightness keys, two tiers each:
//   - media key (NX_KEYTYPE_* from <IOKit/hidsystem/ev_keymap.h>, via the
//     hotkey lib's mediaKeyBit tap) — emitted on Macs with a backlight
//   - plain F-key keycode — emitted in "standard function keys" keyboard
//     mode and on backlight-less Macs (e.g. Mac mini), where macOS never
//     synthesizes brightness media events (verified: media tap caught
//     volume keys but brightness keys only surface as plain F1/F2)
func extraHotkeys() []nativeKeyBinding {
	return []nativeKeyBinding{
		{name: "brightness-down", action: "brightness", value: "-10", cands: []hotkey.Key{
			hotkey.Key(1<<16 | 3), // NX_KEYTYPE_BRIGHTNESS_DOWN
			hotkey.KeyF1,          // plain F1 (0x7A)
		}},
		{name: "brightness-up", action: "brightness", value: "+10", cands: []hotkey.Key{
			hotkey.Key(1<<16 | 2), // NX_KEYTYPE_BRIGHTNESS_UP
			hotkey.KeyF2,          // plain F2 (0x78)
		}},
	}
}
