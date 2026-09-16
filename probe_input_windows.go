//go:build windows

// probe-input: raw-input listener that logs HID consumer-control usages
// (brightness up = 0x6F, down = 0x70) to verify whether the keyboard
// delivers them to user space before monctl maps them natively.
package main

import (
	"flag"
	"fmt"
	"runtime"
	"strings"
	"time"
	"unsafe"
)

// usageName labels the consumer usages we care about.
func usageName(u uint16) string {
	switch u {
	case 0x6F:
		return "0x6F BrightnessUp"
	case 0x70:
		return "0x70 BrightnessDown"
	}
	return fmt.Sprintf("0x%02X", u)
}

var probeEvents int // brightness events seen (reported in the verdict)

// probeDevices returns the top-level collections we listen for:
// consumer control (brightness etc.) and keyboard (F1/F2 scancodes).
func probeDevices(hwnd uintptr) []rawInputDevice {
	return []rawInputDevice{
		{UsUsagePage: pageConsumer, UsUsage: usageConsumerControl, DwFlags: ridevInputSink, HwndTarget: hwnd},
		{UsUsagePage: pageKeyboard, UsUsage: usageKeyboardKeypad, DwFlags: ridevInputSink, HwndTarget: hwnd},
	}
}

func cmdProbeInput(argv []string) int {
	fs := flag.NewFlagSet("probe-input", flag.ContinueOnError)
	dur := fs.Duration("t", 30*time.Second, "how long to listen")
	list := fs.Bool("list", false, "enumerate HID devices and check brightness-usage support, then exit")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	if *list {
		return probeList()
	}
	if *dur <= 0 {
		return fail(fmt.Errorf("-t must be positive (got %v)", *dur))
	}
	runtime.LockOSThread() // window + message pump must stay on one thread

	hwnd, err := createMessageWindow("monctlProbe")
	if err != nil {
		return fail(err)
	}
	if err := registerRawInput(hwnd, probeDevices(hwnd)); err != nil {
		return fail(err)
	}

	fmt.Printf("monctl probe-input: listening %v for consumer 0x0C + keyboard page 0x01 + LL keyboard hook\n", dur)
	fmt.Println("Press the keys under test NOW (nothing else).", dur)
	var hookSeen int
	unhook, hookErr := installKbdHook(func(vk, scan uint32, extended, injected, down bool) {
		hookSeen++
		dir := "up"
		if down {
			dir = "down"
		}
		fmt.Printf("[%s] HOOK VK=0x%02X SC=0x%02X e0=%v injected=%v %s\n",
			time.Now().Format("15:04:05.000"), vk, scan, extended, injected, dir)
	})
	defer unhook()
	if hookErr != nil {
		fmt.Printf("NOTE: keyboard hook unavailable (%v) — HOOK lines below will be empty\n", hookErr)
	}

	_ = pump(*dur, func(_, lParam uintptr) { handleRawInput(lParam) }, nil, func() {
		procPostQuitMessage.Call(0)
	})
	fmt.Println("\n--- probe finished ---")
	fmt.Printf("raw WM_INPUT events seen: %d\n", rawTotal)
	if probeEvents > 0 {
		fmt.Printf("RESULT: brightness keys DETECTED (%d event(s)) — safe to implement native mapping\n", probeEvents)
	} else {
		fmt.Println("RESULT: brightness keys NOT DETECTED — G HUB or the driver is swallowing them; keep explicit hotkeys")
	}
	return 0
}

// probeList enumerates every raw-input HID device and, for consumer-page
// collections, checks which interesting usages (brightness, volume) the
// descriptor actually supports — no key presses needed.
func probeList() int {
	type interesting struct {
		u    uint16
		name string
	}
	checks := []interesting{
		{0x6F, "BrightnessUp"}, {0x70, "BrightnessDown"},
		{0xE9, "VolumeUp"}, {0xEA, "VolumeDown"}, {0xE2, "Mute"},
	}
	type devInfo struct {
		name  string
		page  uint16
		usage uint16
		supp  map[uint16]bool
	}
	var devs []devInfo
	for _, d := range listRawDevices() {
		ppBuf, ok := devicePreparsedData(d.HDevice)
		if !ok {
			continue
		}
		pp := uintptr(unsafe.Pointer(&ppBuf[0]))
		caps, ok := deviceCaps(pp)
		if !ok {
			continue
		}
		di := devInfo{name: rawDeviceName(d.HDevice), page: caps.UsagePage, usage: caps.Usage, supp: map[uint16]bool{}}
		if caps.UsagePage == pageConsumer {
			for _, c := range checks {
				di.supp[c.u] = usageSupported(pp, int(caps.InputReportByteLength), pageConsumer, c.u)
			}
		}
		devs = append(devs, di)
	}
	fmt.Printf("%d raw-input HID collections:\n", len(devs))
	anyBrightness := false
	for _, di := range devs {
		if di.page != pageConsumer {
			continue
		}
		parts := make([]string, 0, len(checks))
		for _, c := range checks {
			mark := "-"
			if di.supp[c.u] {
				mark = "YES"
				if c.u == 0x6F || c.u == 0x70 {
					anyBrightness = true
				}
			}
			parts = append(parts, fmt.Sprintf("%s=%s", c.name, mark))
		}
		fmt.Printf("  [consumer TL-usage 0x%02X] %s  %s\n", di.usage, di.name, strings.Join(parts, " "))
	}
	if anyBrightness {
		fmt.Println("RESULT: brightness usages EXIST in HID descriptors — native mapping should work")
	} else {
		fmt.Println("RESULT: NO consumer collection supports brightness usages — keys are vendor-specific (G HUB territory)")
	}
	return 0
}

// handleRawInput decodes one WM_INPUT message (lParam = HRAWINPUT).
var rawTotal int

func handleRawInput(hRawInput uintptr) {
	rawTotal++
	buf := getRawInputBuffer(hRawInput)
	if buf == nil {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	if len(buf) >= int(unsafe.Sizeof(rawInputHeader{}))+int(unsafe.Sizeof(rawKeyboard{})) &&
		(*(*rawInputHeader)(unsafe.Pointer(&buf[0]))).DwType == 1 { // RIM_TYPEKEYBOARD
		var rk rawKeyboard
		rk = *(*rawKeyboard)(unsafe.Pointer(&buf[unsafe.Sizeof(rawInputHeader{})]))
		if rk.Message == 0x0101 { // WM_KEYUP: keep make only
			return
		}
		// RAWKEYBOARD.Flags: bit0=RI_KEY_BREAK, bit1=RI_KEY_E0, bit2=RI_KEY_E1
		fmt.Printf("[%s] KBD VK=0x%02X SC=0x%02X break=%v e0=%v\n",
			ts, rk.VKey, rk.MakeCode, rk.Flags&1 != 0, rk.Flags&2 != 0)
		return
	}
	hDev, reports := hidReports(buf)
	if reports == nil {
		return
	}
	devName := rawDeviceName(hDev)
	for _, report := range reports {
		cons := hidUsages(hDev, report, pageConsumer)
		kons := hidUsages(hDev, report, pageKeyboard)
		if len(cons) == 0 && len(kons) == 0 {
			if rawTotal <= 20 { // throttle: dump the first few empty reports
				fmt.Printf("[%s] %s empty report=% x\n", ts, devName, report)
			}
			continue // key-release / empty report
		}
		names := make([]string, 0, len(cons)+len(kons))
		for _, u := range cons {
			names = append(names, usageName(u))
			if u == 0x6F || u == 0x70 {
				probeEvents++
			}
		}
		for _, u := range kons {
			names = append(names, fmt.Sprintf("kbd 0x%02X", u))
		}
		fmt.Printf("[%s] %s %s\n", ts, devName, strings.Join(names, ", "))
	}
}
