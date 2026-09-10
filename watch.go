// Hotkey watch daemon: registers global hotkeys and applies monitor actions.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.design/x/hotkey"

	"github.com/bacnh85/monctl/monitor"
)

var hkMod = map[string]hotkey.Modifier{
	"ctrl":    hotkey.ModCtrl,
	"control": hotkey.ModCtrl,
	"shift":   hotkey.ModShift,
	"alt":     hkModAlt,
	"option":  hkModAlt,
	"cmd":     hkModCmd,
	"command": hkModCmd,
	"win":     hkModCmd,
}

var hkKey = map[string]hotkey.Key{
	"up": hotkey.KeyUp, "down": hotkey.KeyDown,
	"left": hotkey.KeyLeft, "right": hotkey.KeyRight,
	// f13/f14: G HUB (or similar) can remap vendor-only keys to emit these;
	// no physical keyboard sends them, so they never collide with typing.
	"f13": hotkey.KeyF13, "f14": hotkey.KeyF14,
	"p":  hotkey.KeyP,
	"f1": hotkey.KeyF1, "f2": hotkey.KeyF2,
	"f3": hotkey.KeyF3, "f4": hotkey.KeyF4,
	"f5": hotkey.KeyF5, "f6": hotkey.KeyF6,
	"f7": hotkey.KeyF7, "f8": hotkey.KeyF8,
	"f9": hotkey.KeyF9, "f10": hotkey.KeyF10,
	"f11": hotkey.KeyF11, "f12": hotkey.KeyF12,
}

// nativeKeyBinding is a built-in (non-config) hotkey binding with per-
// platform candidate keys; every candidate that registers feeds the action.
type nativeKeyBinding struct {
	name   string
	action string
	value  string
	cands  []hotkey.Key
}

// parseHotkey parses "ctrl+alt+up" into (mods, key).
func parseHotkey(spec string) ([]hotkey.Modifier, hotkey.Key, error) {
	var mods []hotkey.Modifier
	var key hotkey.Key
	found := false
	for _, part := range strings.Split(strings.ToLower(spec), "+") {
		if m, ok := hkMod[part]; ok {
			mods = append(mods, m)
			continue
		}
		if k, ok := hkKey[part]; ok {
			key = k
			found = true
			continue
		}
		return nil, 0, fmt.Errorf("unknown key %q in %q", part, spec)
	}
	if !found {
		return nil, 0, fmt.Errorf("no key in %q", spec)
	}
	return mods, key, nil
}

// nativeBrightnessEnabled reports whether F1/F2 media-key interception is
// on (default true; "native_brightness": false in config.json opts out).
func nativeBrightnessEnabled(cfg *monitor.Config) bool {
	return cfg.NativeBrightness == nil || *cfg.NativeBrightness
}

// applyAction performs one hotkey action using the same code path as the
// CLI. Brightness actions additionally flash the native macOS OSD (sun icon
// + level bar) via the bundled osd helper.
func applyAction(ctrl monitor.Controller, hk monitor.Hotkey) error {
	code, ok := monitor.KnownVCPCodes()[strings.ToLower(hk.Action)]
	if !ok {
		return fmt.Errorf("unknown action %q", hk.Action)
	}
	targets, err := monitor.ResolveTargets(ctrl, hk.Target)
	if err != nil {
		return err
	}
	for _, m := range targets {
		pct, err := applySet(ctrl, m, code, strings.ToLower(hk.Action), hk.Value)
		if err != nil {
			return err
		}
		if strings.EqualFold(hk.Action, "brightness") && m.Primary {
			showBrightnessOSD(uint32(m.Handle), pct)
		}
	}
	return nil
}

// showBrightnessOSD displays the native macOS brightness OSD (sun icon +
// level bar) at pct (0..100) on display displayID via the bundled osd
// helper (com.apple.OSDUIHelper, same as MonitorControl/BetterDisplay).
func showBrightnessOSD(displayID uint32, pct int) {
	bin := osdHelperPath()
	if bin == "" {
		return
	}
	_ = exec.Command(bin, fmt.Sprint(displayID), fmt.Sprint(pct)).Start()
}

// osdHelperPath locates the osd helper: inside the monctl.app bundle when
// running from it, else alongside the binary, else tools/osd if built.
func osdHelperPath() string {
	exe, err := os.Executable()
	if err == nil {
		// in-bundle: <bundle>/Contents/MacOS/monctl -> <bundle>/Contents/MacOS/osd
		cand := filepath.Join(filepath.Dir(exe), "osd")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return ""
}

func cmdWatch(argv []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "config path (default: UserConfigDir/monctl/config.json)")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	path := *cfgPath
	if path == "" {
		p, err := monitor.ConfigPath()
		if err != nil {
			return fail(err)
		}
		path = p
	}
	if err := monitor.WriteDefaultConfig(path); err != nil {
		return fail(err)
	}
	cfg, err := monitor.LoadConfig(path)
	if err != nil {
		return fail(err)
	}
	ctrl := monitor.New()

	// Native brightness keys, routed to DDC brightness @all. Enabled by
	// default; "native_brightness": false in the config opts out.
	//   - Windows: raw-input HID consumer-page watcher (usages 0x6F/0x70).
	//   - macOS: intercept the native F1/F2 brightness keys via media-event
	//     taps (two candidate keys per binding — whichever the
	//     hardware/keyboard mode emits gets caught).
	if nativeBrightnessEnabled(cfg) {
		watchNativeBrightness(func(hk monitor.Hotkey) error { return applyAction(ctrl, hk) })
		for _, mk := range extraHotkeys() {
			registered := 0
			for _, key := range mk.cands {
				hk := hotkey.New(nil, key)
				if err := hk.Register(); err != nil {
					fmt.Fprintf(os.Stderr, "monctl watch: %s (key %d) unavailable: %v\n", mk.name, key, err)
					continue
				}
				registered++
				binding := monitor.Hotkey{Keys: mk.name, Action: mk.action, Target: "@all", Value: mk.value}
				firedKey := key // capture for diagnostics
				go func() {
					for range hk.Keydown() {
						if os.Getenv("MONCTL_DEBUG") != "" {
							fmt.Fprintf(os.Stderr, "monctl watch: %s fired via key 0x%X (media=%v)\n", binding.Keys, uint32(firedKey), firedKey&(1<<16) != 0)
						}
						if err := applyAction(ctrl, binding); err != nil {
							fmt.Fprintf(os.Stderr, "monctl watch: %s: %v\n", binding.Keys, err)
						}
					}
				}()
			}
			if registered > 0 {
				fmt.Printf("watching %s -> %s %s (%d key form(s))\n", mk.name, mk.action, mk.value, registered)
			}
		}
	}

	for _, h := range cfg.Hotkeys {
		mods, key, err := parseHotkey(h.Keys)
		if err != nil {
			// Skip bad bindings; log but keep the rest of the daemon alive.
			fmt.Fprintf(os.Stderr, "monctl watch: skipping %q: %v\n", h.Keys, err)
			continue
		}
		hk := hotkey.New(mods, key)
		if err := hk.Register(); err != nil {
			return fail(fmt.Errorf("register %s: %w %s", h.Keys, err, registerHint))
		}
		binding := h // copy for goroutine
		// Input/power actions flip the monitor's KVM. Fire on release,
		// but note the lib's Keyup fires when the NON-modifier key comes
		// up — usually while Ctrl/Alt are still physically held. The
		// monitor's USB hub then re-enumerates mid-combo and the far
		// machine's first HID report has the modifier bits set, leaving
		// a stuck Ctrl/Alt there. So: wait until every modifier is up
		// (HID key state) and let the key-up reports drain before
		// sending the DDC command.
		events := hk.Keydown()
		switchKVM := binding.Action == "input" || binding.Action == "power"
		if switchKVM {
			events = hk.Keyup()
		}
		go func() {
			for range events {
				if switchKVM {
					waitModifiersReleased(2 * time.Second)
				}
				if err := applyAction(ctrl, binding); err != nil {
					fmt.Fprintf(os.Stderr, "monctl watch: %s: %v\n", binding.Keys, err)
				}
			}
		}()
		fmt.Printf("watching %s -> %s %s\n", binding.Keys, binding.Action, binding.Value)
	}
	fmt.Println("monctl watch running; Ctrl+C to quit")
	select {} // block forever; main-thread runloop dispatches hotkey events
}
