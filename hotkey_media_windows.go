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

// lastKeyboardGone reports whether no keyboard device remains attached
// (the departing device is already unlisted when GIDC_REMOVAL fires).
// Holds tracked from other keyboards (second keyboard, YubiKey's keyboard
// TLC) must survive an unrelated removal.
func lastKeyboardGone() bool {
	for _, d := range listRawDevices() {
		if d.DwType != 1 { // RIM_TYPEKEYBOARD
			continue
		}
		ppBuf, ok := devicePreparsedData(d.HDevice)
		if !ok {
			continue
		}
		if caps, ok := deviceCaps(uintptr(unsafe.Pointer(&ppBuf[0]))); ok &&
			caps.UsagePage == pageKeyboard && caps.Usage == usageKeyboardKeypad {
			return false
		}
	}
	return true
}

// sendInputKB is the injection seam; stubbed in tests.
var sendInputKB = func(inp *inputKB) bool {
	r, _, _ := procSendInput.Call(1, uintptr(unsafe.Pointer(inp)), unsafe.Sizeof(*inp))
	return r != 0
}

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
		if heldFor(k, held) {
			continue // pressed on this host — not stuck
		}
		flags := keyup
		if k.ext {
			flags |= extkey
		}
		inp := inputKB{Type: 1, Ki: keybdInput{Vk: k.vk, Scan: k.scan, Flags: flags}}
		if !sendInputKB(&inp) && !scrubWarned {
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
		// lives for the daemon's lifetime on this pumping thread. A failed
		// install means liveKeys stays empty — hold protection degrades to
		// unconditional scrubbing, so say so.
		if _, err := installKbdHook(func(vk, scan uint32, extended, injected, down bool) {
			trackLiveKey(vk, injected, down)
		}); err != nil {
			fmt.Fprintf(os.Stderr, "monctl watch: keyboard hook unavailable (%v) — hold protection degraded\n", err)
		}
		// DDC I/O must run off the pump/LL-hook thread AND be serialized:
		// auto-repeat queues ~30 reports/s and applySet is a
		// read-modify-write per monitor (GetVCP then SetVCP).
		applyCh := newApplyWorker(func(hk monitor.Hotkey) error {
			if apply == nil {
				return nil
			}
			if err := apply(hk); err != nil {
				fmt.Fprintf(os.Stderr, "monctl watch: native brightness: %v\n", err)
			}
			return err
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
					// Never inline: this thread owns the WH_KEYBOARD_LL
					// hook; slow DDC callbacks get it silently dropped.
					submit(applyCh, monitor.Hotkey{Keys: "brightness-native", Action: "brightness", Target: "@all", Value: val})
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
				// Holds marked before the device vanished are gone — but
				// only clear when the LAST keyboard left; other keyboards'
				// holds are still live.
				if lastKeyboardGone() {
					clearLiveKeys()
				}
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
