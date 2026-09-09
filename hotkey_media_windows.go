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
	"unsafe"

	"github.com/bacnh85/monctl/monitor"
)

const (
	usageBrightnessUp   = 0x6F
	usageBrightnessDown = 0x70
)

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
		devs := consumerCollections(hwnd)
		if err := registerRawInput(hwnd, devs); err != nil {
			fmt.Fprintf(os.Stderr, "monctl watch: native brightness: %v\n", err)
			return
		}
		fmt.Printf("watching brightness-up/down (native keys, %d consumer collection(s)) -> brightness ±10\n", len(devs))
		_ = pump(0, func(lParam uintptr) {
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
					hk := monitor.Hotkey{Keys: "brightness-native", Action: "brightness", Target: "@all", Value: val}
					if err := apply(hk); err != nil {
						fmt.Fprintf(os.Stderr, "monctl watch: native brightness: %v\n", err)
					}
				}
			}
		}, func() {})
	}()
}
