//go:build darwin

package monitor

import "testing"

// Packet-level tests for the macOS DDC protocol port (m1ddc wire format).
// Big-endian order and the checksum variants (read: no src address; write:
// src address folded in) are the classic silent-failure bugs.

func TestDDCReadReq(t *testing.T) {
	cases := []struct {
		code byte
		want [4]byte
	}{
		{0x10, [4]byte{0x82, 0x01, 0x10, 0x6E ^ 0x51 ^ 0x82 ^ 0x01 ^ 0x10}}, // brightness
		{0x60, [4]byte{0x82, 0x01, 0x60, 0x6E ^ 0x51 ^ 0x82 ^ 0x01 ^ 0x60}}, // input
		{0xD6, [4]byte{0x82, 0x01, 0xD6, 0x6E ^ 0x51 ^ 0x82 ^ 0x01 ^ 0xD6}}, // power
	}
	for _, c := range cases {
		if got := ddcReadReq(c.code, 0x51); got != c.want {
			t.Errorf("ddcReadReq(0x%02X) = % X, want % X", c.code, got, c.want)
		}
	}
}

func TestDDCWriteReq(t *testing.T) {
	// Verified against m1ddc's prepareDDCWrite for luminance=50, src=0x51:
	// 84 03 10 00 32 | 0x6E ^ 0x51 ^ 84 03 10 00 32 = 0x9A
	got := ddcWriteReq(0x10, 0x51, 50)
	want := [6]byte{0x84, 0x03, 0x10, 0x00, 0x32, 0x9A}
	if got != want {
		t.Errorf("ddcWriteReq = % X, want % X", got, want)
	}
}

func TestParseDDCReply(t *testing.T) {
	// Max in bytes 6..7, current in 8..9, both big-endian.
	buf := make([]byte, 12)
	buf[6], buf[7] = 0x00, 0x32 // max = 50 (G95NC brightness max)
	buf[8], buf[9] = 0x00, 0x19 // cur = 25
	cur, max, ok := parseDDCReply(buf)
	if !ok || cur != 25 || max != 50 {
		t.Errorf("parseDDCReply = (%d,%d,%v), want (25,50,true)", cur, max, ok)
	}
	if _, _, ok := parseDDCReply(make([]byte, 12)); ok {
		t.Error("all-zero reply should be rejected (flaky-panel empty read)")
	}
	if _, _, ok := parseDDCReply(make([]byte, 8)); ok {
		t.Error("truncated reply should be rejected")
	}
}

func TestDDCReplyValid(t *testing.T) {
	// Real frame captured from a G95NC over DP (input request, src 0x50):
	// valid header 6e 88 02 00, code 0x60 echoed at [4], payload 15/50.
	good := []byte{0x6e, 0x88, 0x02, 0x00, 0x60, 0x00, 0x00, 0x32, 0x00, 0x0f, 0xe9, 0x29}
	if !ddcReplyValid(good, 0x60) {
		t.Error("valid frame rejected")
	}
	wrongCode := append([]byte{}, good...)
	wrongCode[4] = 0x10
	if ddcReplyValid(wrongCode, 0x12) {
		t.Error("frame with wrong echoed code accepted")
	}
	badSrc := append([]byte{}, good...)
	badSrc[0] = 0x6D
	if ddcReplyValid(badSrc, 0x60) {
		t.Error("frame with bad source byte accepted")
	}
	if ddcReplyValid(good[:8], 0x60) {
		t.Error("truncated frame accepted")
	}
	zero := append([]byte{}, good...)
	zero[6], zero[7], zero[8], zero[9] = 0, 0, 0, 0
	if ddcReplyValid(zero, 0x60) {
		t.Error("all-zero payload accepted")
	}
}

func TestDDCAttemptAddr(t *testing.T) {
	// Without override: alternate 0x51 / 0x50 across attempts.
	for attempt, want := range map[int]byte{0: 0x51, 1: 0x50, 2: 0x51, 3: 0x50} {
		if got := ddcAttemptAddr(0x10, false, 0, attempt); got != want {
			t.Errorf("ddcAttemptAddr(attempt=%d) = 0x%02X, want 0x%02X", attempt, got, want)
		}
	}
	// 0xF4 (alternate input select) always uses 0x50 when not overridden.
	if got := ddcAttemptAddr(0xF4, false, 0, 0); got != 0x50 {
		t.Errorf("ddcAttemptAddr(0xF4) = 0x%02X, want 0x50", got)
	}
	// Env override wins for everything.
	if got := ddcAttemptAddr(0xF4, true, 0x52, 1); got != 0x52 {
		t.Errorf("ddcAttemptAddr(override) = 0x%02X, want 0x52", got)
	}
}
