//go:build windows

package main

import (
	"time"

	"golang.org/x/sys/windows"
)

var (
	user32Mods      = windows.NewLazySystemDLL("user32.dll")
	procGetAsyncKey = user32Mods.NewProc("GetAsyncKeyState")
)

// Virtual keys of every modifier, including left/right variants.
var modVKs = []int{
	0x10, 0xA0, 0xA1, // Shift, LShift, RShift
	0x11, 0xA2, 0xA3, // Ctrl, LCtrl, RCtrl
	0x12, 0xA4, 0xA5, // Alt, LMenu, RMenu
	0x14,       // CapsLock (sticky modifiers too)
	0x5B, 0x5C, // LWin, RWin
}

// anyModifierPressed reports whether any modifier key is physically held
// (GetAsyncKeyState high bit = currently down).
func anyModifierPressed() bool {
	for _, vk := range modVKs {
		r, _, _ := procGetAsyncKey.Call(uintptr(vk))
		if r&0x8000 != 0 {
			return true
		}
	}
	return false
}

// waitModifiersReleased blocks until no modifier key is physically held
// (up to max), then lets the final key-up events drain.
func waitModifiersReleased(max time.Duration) {
	deadline := time.Now().Add(max)
	for anyModifierPressed() {
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
}
