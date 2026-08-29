// monctl - DDC/CI monitor control CLI (Windows first, macOS planned).
package main
import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/bacnh85/monctl/monitor"
)

const usage = `monctl - control external monitors via DDC/CI

Usage:
  monctl scan                          List monitors with current state
  monctl get   <name> [-m N]           Read brightness|contrast|input|power
  monctl set   <name> <value> [-m N]   Set: number, +N/-N offset, input/power name, or 0xNN
  monctl vcp   <0xcode> [value] [-m N] Raw VCP read/write escape hatch
  monctl watch [--config PATH]         Run hotkey daemon (foreground)
  monctl config                        Write example config if absent, print its path
  monctl autostart [--remove]          Register/unregister watch daemon in HKCU Run key

Examples:
  monctl set brightness 30             all monitors to 30
  monctl set brightness +10            offset, clamped
  monctl set input hdmi2               switch source
  monctl set power off                 sleep the panel
`

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "scan":
		return cmdScan()
	case "get", "set":
		return cmdGetSet(args[0], args[1:])
	case "vcp":
		return cmdVCP(args[1:])
	case "watch":
		return cmdWatch(args[1:])
	case "config":
		return cmdConfig()
	case "autostart":
		return cmdAutostart(args[1:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func fail(err error) int {
	fmt.Fprintf(os.Stderr, "monctl: %v\n", err)
	return 1
}

// resolveTargets applies -m selection (0 = all).
func resolveTargets(ctrl monitor.Controller, mIdx int) ([]monitor.Info, error) {
	sel := "@all"
	if mIdx > 0 {
		sel = strconv.Itoa(mIdx)
	}
	return monitor.ResolveTargets(ctrl, sel)
}

func cmdScan() int {
	ctrl := monitor.New()
	infos, err := ctrl.List()
	if err != nil {
		return fail(err)
	}
	for _, m := range infos {
		inputCur, _, ierr := ctrl.GetVCP(m, monitor.VCPInput)
		powerCur, _, perr := ctrl.GetVCP(m, monitor.VCPPower)
		state := "input=? power=?"
		switch {
		case ierr == nil && perr == nil:
			in := monitor.InputName(byte(inputCur))
			if in == "" {
				in = fmt.Sprintf("0x%02X", inputCur)
			}
			state = fmt.Sprintf("input=%s power=%d", in, powerCur)
		case ierr != nil:
			state = fmt.Sprintf("DDC unavailable: %v", ierr)
		}
		fmt.Printf("[%d] %s primary=%v %s\n", m.Index, m.Description, m.Primary, state)
	}
	return 0
}

func cmdGetSet(verb string, argv []string) int {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	mIdx := fs.Int("m", 0, "monitor index (0 = all)")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	name := strings.ToLower(rest[0])
	code, ok := monitor.KnownVCPCodes()[name]
	if !ok {
		return fail(fmt.Errorf("unknown control %q (brightness|contrast|input|power)", name))
	}
	var rawVal string
	if verb == "set" {
		if len(rest) < 2 {
			return fail(fmt.Errorf("set %s requires a value", name))
		}
		rawVal = rest[1]
	}

	ctrl := monitor.New()
	targets, err := resolveTargets(ctrl, *mIdx)
	if err != nil {
		return fail(err)
	}

	for _, m := range targets {
		if verb == "get" {
			cur, maxV, err := ctrl.GetVCP(m, code)
			if err != nil {
				return fail(err)
			}
			extra := ""
			switch code {
			case monitor.VCPInput:
				if n := monitor.InputName(byte(cur)); n != "" {
					extra = fmt.Sprintf(" (%s)", n)
				}
			case monitor.VCPPower:
				extra = fmt.Sprintf(" (%s)", powerName(byte(cur)))
			}
			fmt.Printf("[%d] %s = %d/%d%s\n", m.Index, name, cur, maxV, extra)
			continue
		}
		if _, err := applySet(ctrl, m, code, name, rawVal); err != nil {
			return fail(err)
		}
		fmt.Printf("[%d] %s = %s ok\n", m.Index, name, rawVal)
	}
	return 0
}

// applySet handles "+N"/"-N" offsets and absolute values.
// Shared by CLI and watch daemon.
// Continuous controls (brightness/contrast): inputs are PERCENT 0-100,
// scaled to the panel-reported max (this G95NC reports max=50).
func applySet(ctrl monitor.Controller, m monitor.Info, code byte, name, rawVal string) (int, error) {
	continuous := name == "brightness" || name == "contrast"
	if strings.HasPrefix(rawVal, "+") || strings.HasPrefix(rawVal, "-") {
		cur, maxV, err := ctrl.GetVCP(m, code)
		if err != nil {
			return 0, err
		}
		delta, err := strconv.Atoi(rawVal)
		if err != nil {
			return 0, fmt.Errorf("bad offset %q", rawVal)
		}
		hi := maxV
		if hi <= 0 {
			hi = 100
		}
		// Offset is expressed in percent points.
		raw := cur + delta*hi/100
		v := byte(clamp(raw, 0, hi))
		return percent(int(v), hi), ctrl.SetVCP(m, code, v)
	}
	b, err := monitor.ParseVCPValue(rawVal)
	if err != nil {
		return 0, err
	}
	if continuous {
		_, maxV, err := ctrl.GetVCP(m, code)
		if err != nil {
			return 0, err
		}
		if b > 100 {
			return 0, fmt.Errorf("value %d out of range 0-100", b)
		}
		hi := maxV
		if hi <= 0 {
			hi = 100
		}
		b = byte(int(b) * hi / 100)
		pct := percent(int(b), hi)
		return pct, ctrl.SetVCP(m, code, b)
	}
	return 0, ctrl.SetVCP(m, code, b)
}

func percent(v int, hi int) int {
	if hi == 0 {
		return 0
	}
	return v * 100 / hi
}

func cmdVCP(argv []string) int {
	fs := flag.NewFlagSet("vcp", flag.ContinueOnError)
	mIdx := fs.Int("m", 0, "monitor index (0 = all)")
	if err := fs.Parse(argv); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	code, err := parseHexOrDec(rest[0])
	if err != nil {
		return fail(fmt.Errorf("bad VCP code %q", rest[0]))
	}
	ctrl := monitor.New()
	targets, err := resolveTargets(ctrl, *mIdx)
	if err != nil {
		return fail(err)
	}
	for _, m := range targets {
		if len(rest) < 2 {
			cur, maxV, err := ctrl.GetVCP(m, code)
			if err != nil {
				return fail(err)
			}
			fmt.Printf("[%d] 0x%02X = %d (max %d) 0x%02X\n", m.Index, code, cur, maxV, cur)
			continue
		}
		val, err := parseHexOrDec(rest[1])
		if err != nil {
			return fail(fmt.Errorf("bad value %q", rest[1]))
		}
		if err := ctrl.SetVCP(m, code, val); err != nil {
			return fail(err)
		}
		fmt.Printf("[%d] 0x%02X = 0x%02X ok\n", m.Index, code, val)
	}
	return 0
}

func parseHexOrDec(s string) (byte, error) {
	n, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(s), "0x"), 16, 8)
	if err != nil {
		n2, err2 := strconv.ParseUint(s, 10, 8)
		if err2 != nil {
			return 0, err
		}
		n = n2
	}
	return byte(n), nil
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func powerName(v byte) string {
	switch v {
	case monitor.PowerOn:
		return "on"
	case monitor.PowerOff:
		return "off"
	case monitor.PowerStandby:
		return "standby"
	}
	return fmt.Sprintf("0x%02X", v)
}
