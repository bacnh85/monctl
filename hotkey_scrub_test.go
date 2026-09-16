//go:build windows

package main

import (
	"runtime"
	"testing"
	"unsafe"
)

// TestInputKBSize pins the Win32 INPUT layout per architecture: SendInput
// rejects a wrong cbSize silently (returns 0), so a struct-field typo must
// fail here rather than no-op the scrub at runtime.
func TestInputKBSize(t *testing.T) {
	want := uintptr(28) // 386/arm: 4 + KEYBDINPUT(16) + 8
	switch runtime.GOARCH {
	case "amd64", "arm64": // union aligned to 8
		want = 40 // 4+4 + KEYBDINPUT(24, padded) + 8
	}
	if got := unsafe.Sizeof(inputKB{}); got != want {
		t.Fatalf("INPUT size = %d on %s, want %d", got, runtime.GOARCH, want)
	}
}

// TestScrubKeyTable checks table invariants: no duplicate VKs, and the
// extended flag only on E0-prefixed scancodes (0x1D/0x38 ctrl/alt, 0x5B/0x5C win).
func TestScrubKeyTable(t *testing.T) {
	e0 := map[uint16]bool{0x1D: true, 0x38: true, 0x5B: true, 0x5C: true}
	seen := map[uint16]bool{}
	for _, k := range stuckScrubKeys {
		if seen[k.vk] {
			t.Errorf("duplicate VK 0x%02X in scrub table", k.vk)
		}
		seen[k.vk] = true
		if k.ext && !e0[k.scan] {
			t.Errorf("VK 0x%02X marked extended but scancode 0x%02X is not E0", k.vk, k.scan)
		}
		if !k.ext && e0[k.scan] && k.vk != 0x09 {
			t.Errorf("VK 0x%02X uses E0 scancode 0x%02X without the extended flag", k.vk, k.scan)
		}
	}
}

// TestScrubPlan pins the WM_INPUT_DEVICE_CHANGE wParam codes against raw
// literals so swapped or wrong gidc constants fail here.
func TestScrubPlan(t *testing.T) {
	if removal, ok := scrubPlan(1); !ok || removal { // GIDC_ARRIVAL
		t.Fatalf("wParam 1 (arrival): got removal=%v ok=%v", removal, ok)
	}
	if removal, ok := scrubPlan(2); !ok || !removal { // GIDC_REMOVAL
		t.Fatalf("wParam 2 (removal): got removal=%v ok=%v", removal, ok)
	}
	for _, w := range []uintptr{0, 3, 99} {
		if _, ok := scrubPlan(w); ok {
			t.Fatalf("wParam %d should be ignored", w)
		}
	}
}

// TestTrackKeyboardRawInjected pins the user-held marker: PHYSICAL key-downs
// mark liveKeys (protecting them from scrub passes), but software-injected
// downs (LLKHF_INJECTED in ExtraInformation — the agent re-injection class)
// must not, or later passes would skip exactly the stuck keys.
func TestTrackKeyboardRawInjected(t *testing.T) {
	mk := func(dwType, msg, vk, extra uint32) []byte {
		buf := make([]byte, unsafe.Sizeof(rawInputHeader{})+unsafe.Sizeof(rawKeyboard{}))
		(*rawInputHeader)(unsafe.Pointer(&buf[0])).DwType = dwType
		rk := (*rawKeyboard)(unsafe.Pointer(&buf[unsafe.Sizeof(rawInputHeader{})]))
		rk.Message, rk.VKey, rk.ExtraInformation = msg, uint16(vk), extra
		return buf
	}
	clearLiveKeys()
	defer clearLiveKeys()

	if !trackKeyboardRaw(mk(1, 0x0100, 0xA2, 0x10)) { // Ctrl down, injected
		t.Fatal("keyboard-type buffer not recognized")
	}
	if liveKeys[0xA2] {
		t.Fatal("injected key-down must not mark liveKeys")
	}
	if !trackKeyboardRaw(mk(1, 0x0100, 0xA2, 0)) { // Ctrl down, physical
		t.Fatal("keyboard-type buffer not recognized")
	}
	if !liveKeys[0xA2] {
		t.Fatal("physical key-down should mark liveKeys")
	}
	trackKeyboardRaw(mk(1, 0x0101, 0xA2, 0)) // Ctrl up
	if liveKeys[0xA2] {
		t.Fatal("key-up should clear the liveKeys marker")
	}
	if trackKeyboardRaw(mk(2, 0x0100, 0xA2, 0)) { // RIM_TYPEHID: not keyboard
		t.Fatal("HID-type buffer must not be treated as keyboard")
	}
	if trackKeyboardRaw([]byte{0}) { // too short
		t.Fatal("truncated buffer must be rejected")
	}
}
