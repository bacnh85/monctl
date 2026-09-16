//go:build windows

// Native brightness keys on Windows: brightness up/down arrive as HID
// consumer-page usages (0x6F/0x70), not VK codes, so RegisterHotKey can't
// see them. We listen via raw input (RIDEV_INPUTSINK — background, no
// focus needed) and feed the same applyAction path as config hotkeys.
// If the vendor driver/G HUB swallows the usages before user space, no
// events arrive and the watcher is simply inert.
package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/bacnh85/monctl/monitor"
)

const (
	usageBrightnessUp   = 0x6F
	usageBrightnessDown = 0x70
)

// stuckScrubKeys are the VKs that a monitor-KVM switch leaves latched on
// the host that loses the keyboard mid-keystroke, plus phantom reports the
// arriving host can see at enumeration. Right Alt (AltGr) shows up on
// Windows as BOTH Ctrl and Alt stuck — matches "Ctrl, Alt or Tab" reports.
var stuckScrubKeys = []struct {
	vk, gen, scan uint16
	ext           bool // E0-prefixed scancode
}{
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

// keybdInput is KEYBDINPUT; inputKB wraps it in INPUT. Padding is derived
// from the pointer width so the layout is exact on 386 (28B) and 64-bit
// (40B) — SendInput validates cbSize against sizeof(INPUT).
type keybdInput struct {
	Vk, Scan  uint16
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type inputKB struct {
	Type uint32                               // INPUT_KEYBOARD
	_    [unsafe.Alignof(uintptr(0)) - 4]byte // union alignment (0 on 386, 4 on 64-bit)
	Ki   keybdInput
	_    [8]byte // MOUSEINPUT (largest union member) exceeds KEYBDINPUT by 8
}

// liveKeys tracks VKs PHYSICALLY pressed on THIS host, fed from the
// low-level keyboard hook — KBDLLHOOKSTRUCT.Flags&LLKHF_INJECTED is the
// documented injected signal (MSDN); RAWKEYBOARD.ExtraInformation is an
// undocumented per-event cookie and must not be trusted for this.
// Software re-injections (G HUB / Options+ down-without-up phantoms) are
// excluded on purpose — they must stay scrub-eligible, or the protection
// would shield exactly the stuck keys it exists to clear.
var (
	liveMu   sync.Mutex
	liveKeys = map[uint16]bool{}
)

func trackLiveKey(vk uint32, injected, down bool) {
	liveMu.Lock()
	if down {
		if !injected {
			liveKeys[uint16(vk)] = true
		}
	} else {
		delete(liveKeys, uint16(vk))
	}
	liveMu.Unlock()
}

func clearLiveKeys() {
	liveMu.Lock()
	liveKeys = map[uint16]bool{}
	liveMu.Unlock()
}

// scrubPlan classifies a WM_INPUT_DEVICE_CHANGE wParam: GIDC codes only.
func scrubPlan(wParam uintptr) (removal, ok bool) {
	switch wParam {
	case gidcArrival:
		return false, true
	case gidcRemoval:
		return true, true
	}
	return false, false
}

var (
	scrubRunning atomic.Bool
	scrubWarned  bool // SendInput failure logged once
)

// scrubStuckKeys injects key-up events for every scrub VK not currently
// held per liveKeys. Key-up for a key already up is a no-op; a stuck
// key's async state is cleared.
func scrubStuckKeys() {
	liveMu.Lock()
	held := make(map[uint16]bool, len(liveKeys))
	for vk := range liveKeys {
		held[vk] = true
	}
	liveMu.Unlock()
	const keyup uint32 = 0x0002  // KEYEVENTF_KEYUP
	const extkey uint32 = 0x0001 // KEYEVENTF_EXTENDEDKEY
	for _, k := range stuckScrubKeys {
		if held[k.vk] || held[k.gen] {
			continue // pressed on this host — not stuck
		}
		flags := keyup
		if k.ext {
			flags |= extkey
		}
		inp := inputKB{Type: 1, Ki: keybdInput{Vk: k.vk, Scan: k.scan, Flags: flags}}
		if r, _, _ := procSendInput.Call(1, uintptr(unsafe.Pointer(&inp)), unsafe.Sizeof(inp)); r == 0 && !scrubWarned {
			scrubWarned = true
			fmt.Fprintln(os.Stderr, "monctl watch: SendInput key-up failed — stuck-key scrub unavailable")
		}
	}
}

// scrubAfterSwitch clears stuck keys around a KVM USB switch. Arrival:
// three passes — enumeration phantoms land within ms, Logitech agent
// re-injection within seconds. Removal: one pass — the leaving host keeps
// async-down state for keys held when the keyboard vanished. Tune delays
// here if probe-input shows later injections.
func scrubAfterSwitch(removal bool) {
	delays := []time.Duration{0, time.Second, 3 * time.Second}
	if removal {
		delays = delays[:1]
	}
	for _, d := range delays {
		if d > 0 {
			time.Sleep(d)
		}
		scrubStuckKeys()
	}
}

// extraHotkeys is macOS-only here (F1/F2 media events via golang.design/x/hotkey).
func extraHotkeys() []nativeKeyBinding { return nil }

// consumerCollections enumerates the distinct top-level collections on the
// consumer usage page. Brightness usages can live beyond the classic
// 0x0C:0x01 (consumer control) collection — e.g. vendor keyboards expose
// extra consumer TLCs — and raw input only delivers events for registered
// (page, usage) pairs.
func consumerCollections(hwnd uintptr) []rawInputDevice {
	seen := map[uint16]bool{usageConsumerControl: true} // always include the classic
	for _, d := range listRawDevices() {
		ppBuf, ok := devicePreparsedData(d.HDevice)
		if !ok {
			continue
		}
		caps, ok := deviceCaps(uintptr(unsafe.Pointer(&ppBuf[0])))
		if ok && caps.UsagePage == pageConsumer {
			seen[caps.Usage] = true
		}
	}
	devs := make([]rawInputDevice, 0, len(seen))
	for u := range seen {
		devs = append(devs, rawInputDevice{UsUsagePage: pageConsumer, UsUsage: u, DwFlags: ridevInputSink, HwndTarget: hwnd})
	}
	return devs
}

// keyboardWithNotify registers the keyboard top-level collection with
// RIDEV_DEVNOTIFY so the pump hears WM_INPUT_DEVICE_CHANGE when the
// monitor's KVM moves the keyboard to or from this host — every switch,
// hotkey- or OSD-triggered.
func keyboardWithNotify(hwnd uintptr) rawInputDevice {
	return rawInputDevice{UsUsagePage: pageKeyboard, UsUsage: usageKeyboardKeypad,
		DwFlags: ridevInputSink | ridevDevNotify, HwndTarget: hwnd}
}

// watchNativeBrightness starts (in a goroutine) the raw-input consumer-page
// listener and maps brightness up/down to brightness +/-10 on @all.
// No-op on non-Windows platforms (see hotkey_media_other.go).
func watchNativeBrightness(apply func(hk monitor.Hotkey) error) {
	go func() {
		runtime.LockOSThread() // window + message pump must stay on one thread
		hwnd, err := createMessageWindow("monctlBrightness")
		if err != nil {
			fmt.Fprintf(os.Stderr, "monctl watch: native brightness: %v\n", err)
			return
		}
		devs := append(consumerCollections(hwnd), keyboardWithNotify(hwnd))
		if err := registerRawInput(hwnd, devs); err != nil {
			fmt.Fprintf(os.Stderr, "monctl watch: native brightness: %v\n", err)
			return
		}
		// Mark user-held keys from the LL hook (documented injected bit);
		// lives for the daemon's lifetime on this pumping thread.
		_ = installKbdHook(func(vk, scan uint32, extended, injected, down bool) {
			trackLiveKey(vk, injected, down)
		})
		if apply == nil {
			fmt.Println("native brightness disabled (native_brightness:false) — raw-input listener kept for KVM stuck-key scrub only")
		} else {
			fmt.Printf("watching brightness-up/down (native keys, %d consumer collection(s)) -> brightness ±10\n", len(devs))
		}
		_ = pump(0, func(wParam, lParam uintptr) {
			buf := getRawInputBuffer(lParam)
			hDev, reports := hidReports(buf)
			for _, rep := range reports {
				usages := hidUsages(hDev, rep, pageConsumer)
				if len(usages) == 0 {
					continue // release / empty report
				}
				if debug := os.Getenv("MONCTL_DEBUG") != ""; debug {
					names := make([]string, len(usages))
					for i, u := range usages {
						names[i] = fmt.Sprintf("0x%02X", u)
					}
					fmt.Fprintf(os.Stderr, "monctl watch: consumer usages %s from %s\n", strings.Join(names, ","), rawDeviceName(hDev))
				}
				for _, u := range usages {
					val := ""
					switch u {
					case usageBrightnessUp:
						val = "+10"
					case usageBrightnessDown:
						val = "-10"
					default:
						continue
					}
					if apply == nil {
						continue
					}
					hk := monitor.Hotkey{Keys: "brightness-native", Action: "brightness", Target: "@all", Value: val}
					if err := apply(hk); err != nil {
						fmt.Fprintf(os.Stderr, "monctl watch: native brightness: %v\n", err)
					}
				}
			}
		}, func(wParam, lParam uintptr) {
			// The monitor's KVM just moved the keyboard to/from this
			// host (happens on input switches whether triggered by
			// hotkey or OSD). Scrub stuck/phantom modifier+Tab state.
			removal, ok := scrubPlan(wParam)
			if !ok {
				return
			}
			what := "arrived"
			if removal {
				what = "left"
				clearLiveKeys() // holds marked before the device vanished are gone
			}
			name := rawDeviceName(lParam) // handle is valid now, not after the sleep
			if !scrubRunning.CompareAndSwap(false, true) {
				return // a scrub sequence is already absorbing this switch
			}
			go func() {
				defer scrubRunning.Store(false)
				scrubAfterSwitch(removal)
				fmt.Printf("monctl watch: keyboard %s (%s) — scrubbed stuck-key state\n", what, name)
			}()
		}, func() {})
	}()
}
