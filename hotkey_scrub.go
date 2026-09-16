// Stuck-key scrub table and hold-protection logic. Pure and untagged so
// it is unit-tested on every CI runner, not only Windows. The Win32
// injection itself lives in hotkey_media_windows.go.
package main

// scrubKey is one stuck-key scrub entry. gen is the generic counterpart
// VK (== vk for keys that have no left/right split).
type scrubKey struct {
	vk, gen, scan uint16
	ext           bool // E0-prefixed scancode
}

// stuckScrubKeys are the VKs that a monitor-KVM switch leaves latched on
// the host that loses the keyboard mid-keystroke, plus phantom reports the
// arriving host can see at enumeration. Right Alt (AltGr) shows up on
// Windows as BOTH Ctrl and Alt stuck — matches "Ctrl, Alt or Tab" reports.
var stuckScrubKeys = []scrubKey{
	{0xA2, 0x11, 0x1D, false}, // LCtrl (gen = VK_CONTROL)
	{0xA3, 0x11, 0x1D, true},  // RCtrl
	{0x11, 0x11, 0x1D, false}, // Ctrl
	{0xA0, 0x10, 0x2A, false}, // LShift (gen = VK_SHIFT)
	{0xA1, 0x10, 0x36, false}, // RShift
	{0x10, 0x10, 0x2A, false}, // Shift
	{0xA4, 0x12, 0x38, false}, // LAlt (gen = VK_MENU)
	{0xA5, 0x12, 0x38, true},  // RAlt (AltGr)
	{0x12, 0x12, 0x38, false}, // Alt
	{0x5B, 0x5B, 0x5B, true},  // LWin
	{0x5C, 0x5C, 0x5C, true},  // RWin
	{0x09, 0x09, 0x0F, false}, // Tab
}

// genFamily maps generic modifier VKs to the specific VKs the LL hook
// actually reports (it never emits the generic 0x10/0x11/0x12). A generic
// scrub entry shares its family scancode, so it must be skipped whenever
// any family member is held — its keyup would release the held key.
var genFamily = map[uint16][]uint16{
	0x11: {0xA2, 0xA3}, // Ctrl
	0x10: {0xA0, 0xA1}, // Shift
	0x12: {0xA4, 0xA5}, // Alt
}

// heldFor reports whether a scrub entry targets a key the user holds:
// the entry VK itself, or — for generic entries — any L/R-specific
// family member.
func heldFor(k scrubKey, held map[uint16]bool) bool {
	if held[k.vk] {
		return true
	}
	for _, s := range genFamily[k.gen] {
		if held[s] {
			return true
		}
	}
	return false
}
