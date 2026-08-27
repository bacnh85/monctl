// Package monitor provides cross-platform DDC/CI access to physical monitors.
// This file defines VCP (Virtual Control Panel) codes and the input-source
// name <-> hex mapping, seeded from the MCCS standard table and calibrated
// live against the connected panel (plan Step 0).
package monitor

import (
	"fmt"
	"strconv"
)

// VCP codes (MCCS standard).
const (
	VCPBrightness = 0x10
	VCPContrast   = 0x12
	VCPInput      = 0x60 // Input source select
	VCPPower      = 0xD6 // Power mode: 1=on, 4=off/sleep via DDC, 5=standby
)

// Power mode values for VCP 0xD6.
const (
	PowerOn      = 0x01
	PowerOff     = 0x04 // monitor enters sleep; wake requires OSD button or input signal
	PowerStandby = 0x05
)

// vcpInputs maps friendly names to VCP 0x60 values.
// MCCS-standard table; Samsung G95NC deviations are corrected during Step-0
// calibration (see plan) — raw hex always accepted as passthrough.
// parseByte parses byte values: "0xNN" hex, otherwise decimal.
func parseByte(s string) (byte, error) {
	if len(s) > 2 && s[:2] == "0x" {
		n, err := strconv.ParseUint(s[2:], 16, 8)
		return byte(n), err
	}
	n, err := strconv.ParseUint(s, 10, 8)
	return byte(n), err
}

// VCP code names accepted by get/set.
var namedCodes = map[string]byte{
	"brightness": VCPBrightness,
	"contrast":   VCPContrast,
	"input":      VCPInput,
	"power":      VCPPower,
}

// vcpInputs maps friendly names to VCP 0x60 values.
// MCCS-standard table; calibrated against the connected panel before shipping.
var vcpInputs = map[string]byte{
	"vga":    0x01,
	"dvi":    0x03,
	"dp1":    0x0F,
	"dp2":    0x10,
	"hdmi1":  0x11,
	"hdmi2":  0x12,
	"usb-c":  0x18, // Samsung-typical USB-C/Type-C upstream
	"usb-c1": 0x18,
	"usb-c2": 0x19,
}

var vcpInputNames = map[byte]string{
	0x01: "vga",
	0x03: "dvi",
	0x0F: "dp1",
	0x10: "dp2",
	0x11: "hdmi1",
	0x12: "hdmi2",
	0x18: "usb-c",
	0x19: "usb-c2",
}

// ParseVCPValue parses a value that is either a named input ("hdmi2"),
// a power name ("on"/"off"/"standby"), or raw hex/decimal ("0x12", "18").
func ParseVCPValue(s string) (byte, error) {
	if b, ok := vcpInputs[s]; ok {
		return b, nil
	}
	switch s {
	case "on":
		return PowerOn, nil
	case "off":
		return PowerOff, nil
	case "standby":
		return PowerStandby, nil
	}
	b, err := parseByte(s)
	if err != nil {
		return 0, fmt.Errorf("invalid value %q", s)
	}
	return b, nil
}

// InputName returns the friendly name for a VCP 0x60 value, or "" if unknown.
func InputName(b byte) string { return vcpInputNames[b] }

// KnownVCPCodes is the set of named control codes accepted by get/set.
func KnownVCPCodes() map[string]byte {
	return map[string]byte{
		"brightness": VCPBrightness,
		"contrast":   VCPContrast,
		"input":      VCPInput,
		"power":      VCPPower,
	}
}
