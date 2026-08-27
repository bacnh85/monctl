package monitor

import "testing"

func TestParseVCPValue(t *testing.T) {
	cases := []struct {
		in   string
		want byte
		ok   bool
	}{
		{"hdmi2", 0x12, true},
		{"dp1", 0x0F, true},
		{"vga", 0x01, true},
		{"usb-c", 0x18, true},
		{"off", PowerOff, true},
		{"on", PowerOn, true},
		{"standby", PowerStandby, true},
		{"0x12", 0x12, true},
		{"18", 18, true},
		{"255", 255, true},
		{"256", 0, false},
		{"bogus", 0, false},
	}
	for _, c := range cases {
		got, err := ParseVCPValue(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("ParseVCPValue(%q) = %#x, %v; want %#x", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("ParseVCPValue(%q) expected error", c.in)
		}
	}
}

func TestInputNameRoundTrip(t *testing.T) {
	for name, code := range vcpInputs {
		if back := InputName(code); back == "" {
			t.Errorf("InputName(0x%02X) empty for name %q", code, name)
		}
	}
	if InputName(0xEE) != "" {
		t.Error("unknown code should map to empty string")
	}
}
