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

// TestTrackLiveKeyInjected pins the user-held marker: PHYSICAL key-downs
// mark liveKeys (protecting them from scrub passes), but software-injected
// downs (KBDLLHOOKSTRUCT.Flags&LLKHF_INJECTED — the documented bit, fed by
// the LL hook) must not, or later passes would skip exactly the stuck keys.
func TestTrackLiveKeyInjected(t *testing.T) {
	clearLiveKeys()
	defer clearLiveKeys()

	trackLiveKey(0xA2, true, true) // injected down (agent re-injection)
	if liveKeys[0xA2] {
		t.Fatal("injected key-down must not mark liveKeys")
	}
	trackLiveKey(0xA2, false, true) // physical down
	if !liveKeys[0xA2] {
		t.Fatal("physical key-down should mark liveKeys")
	}
	trackLiveKey(0xA2, false, false) // physical up
	if liveKeys[0xA2] {
		t.Fatal("key-up should clear the liveKeys marker")
	}
	trackLiveKey(0xA0, true, true)  // injected down
	trackLiveKey(0xA0, true, false) // matching injected up
	if liveKeys[0xA0] {
		t.Fatal("injected up must not leave a marker")
	}
}

// TestScrubHoldProtection pins the hold protection end to end: a held
// specific VK must suppress its own AND its generic-family keyup — the
// generic entry shares the family scancode, so emitting it releases the
// held key (regression: the old held[k.gen] check was dead because the
// LL hook only reports L/R-specific VKs).
func TestScrubHoldProtection(t *testing.T) {
	var emitted []uint16
	saved := sendInputKB
	sendInputKB = func(inp *inputKB) bool {
		emitted = append(emitted, inp.Ki.Vk)
		return true
	}
	defer func() { sendInputKB = saved }()

	for _, c := range []struct{ heldSpecific, generic uint16 }{
		{0xA2, 0x11}, {0xA0, 0x10}, {0xA4, 0x12}, // LCtrl, LShift, LAlt
	} {
		clearLiveKeys()
		liveKeys[c.heldSpecific] = true
		emitted = nil
		scrubStuckKeys()
		for _, vk := range emitted {
			if vk == c.generic {
				t.Fatalf("held 0x%02X but scrub emitted generic family keyup 0x%02X", c.heldSpecific, vk)
			}
			if vk == c.heldSpecific {
				t.Fatalf("held 0x%02X but scrub emitted its own keyup", c.heldSpecific)
			}
		}
	}

	// Control: nothing held — every scrub entry emits exactly once.
	clearLiveKeys()
	emitted = nil
	scrubStuckKeys()
	if len(emitted) != len(stuckScrubKeys) {
		t.Fatalf("empty liveKeys: emitted %d keyups, want %d", len(emitted), len(stuckScrubKeys))
	}
}
