// Pure scrub-table/held-key logic tests — no build tag, so they run on
// every CI runner, not only Windows.
package main

import "testing"

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
		// NOTE: the reverse does not hold — 0x1D/0x38 are shared by the
		// non-extended L-variants (LCtrl, LAlt) and extended R-variants.
		if fam := genFamily[k.gen]; k.gen != k.vk && len(fam) == 0 {
			t.Errorf("generic VK 0x%02X missing from genFamily", k.gen)
		}
	}
}

// TestHeldForFamily pins family suppression: the LL hook only reports
// L/R-specific VKs, so a generic entry must be held-suppressed whenever
// any family member is held.
func TestHeldForFamily(t *testing.T) {
	for _, c := range []struct {
		generic, specific uint16
	}{
		{0x11, 0xA2}, {0x11, 0xA3}, // Ctrl family
		{0x10, 0xA0}, {0x10, 0xA1}, // Shift family
		{0x12, 0xA4}, {0x12, 0xA5}, // Alt family
	} {
		entry := scrubKey{vk: c.generic, gen: c.generic, scan: 0xFF}
		if !heldFor(entry, map[uint16]bool{c.specific: true}) {
			t.Errorf("generic 0x%02X not suppressed while family member 0x%02X held", c.generic, c.specific)
		}
		// Specific entries are suppressed by their own VK only.
		if !heldFor(scrubKey{vk: c.specific, gen: c.generic}, map[uint16]bool{c.specific: true}) {
			t.Errorf("specific 0x%02X not suppressed while held", c.specific)
		}
	}
	// Unrelated holds don't suppress.
	if heldFor(scrubKey{vk: 0x11, gen: 0x11}, map[uint16]bool{0xA0: true}) {
		t.Error("Ctrl entry suppressed by unrelated held Shift")
	}
	if heldFor(scrubKey{vk: 0x09, gen: 0x09}, map[uint16]bool{0xA2: true, 0xA0: true, 0xA4: true}) {
		t.Error("Tab entry suppressed by held modifiers")
	}
}
