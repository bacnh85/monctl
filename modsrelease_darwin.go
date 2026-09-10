//go:build darwin

package main

/*
#cgo LDFLAGS: -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>
*/
import "C"

import "time"

// Carbon virtual keycodes of every modifier key (HIToolbox Events.h).
var modKeycodes = []C.CGKeyCode{
	0x37, // kVK_Command
	0x36, // kVK_RightCommand
	0x38, // kVK_Shift
	0x3C, // kVK_RightShift
	0x3A, // kVK_Option
	0x3D, // kVK_RightOption
	0x3B, // kVK_Control
	0x3E, // kVK_RightControl
	0x3F, // kVK_Function
}

// anyModifierPressed reports whether any modifier key is physically held,
// read from the HID system state (independent of the hotkey event tap).
func anyModifierPressed() bool {
	for _, k := range modKeycodes {
		if C.CGEventSourceKeyState(C.kCGEventSourceStateHIDSystemState, k) {
			return true
		}
	}
	return false
}

// waitModifiersReleased blocks until no modifier key is physically held
// (up to max), then lets the final key-up HID reports drain.
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
