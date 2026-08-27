# monctl

DDC/CI monitor control CLI for Windows (macOS planned). Single Go binary —
scan, control brightness/contrast/input/power, bind global hotkeys.

Verified live on: Samsung Odyssey G95NC via HDMI-2 (input `0x12`,
brightness VCP max=50).

## Build

    go build -o bin/monctl.exe .
    go build -ldflags "-H windowsgui" -o bin/monctl-tray.exe .   # silent bg build

## Usage

    monctl scan                        # list monitors (EDID model) + input/power
    monctl get  brightness|contrast|input|power [-m N]
    monctl set  <name> <value> [-m N]  # value: percent, +N/-N offset, name, or 0xNN
    monctl vcp  <0xcode> [value]       # raw DDC escape hatch
    monctl watch [--config PATH]       # hotkey daemon (foreground)
    monctl config                      # write example config, print path
    monctl autostart [--remove]        # HKCU Run-key registration

Brightness/contrast values are **percent 0-100**, scaled to the panel-reported
VCP max (G95NC reports 50). Inputs: vga dvi dp1 dp2 hdmi1 hdmi2 usb-c.
Power: on off standby. Requires monitor OSD setting **DDC/CI: Enabled**.

Examples:

    monctl set brightness 30
    monctl set brightness +10
    monctl set input hdmi2
    monctl set power off

## Hotkeys

`monctl watch` reads `%APPDATA%\monctl\config.json` (written by `monctl config`):

    {"hotkeys":[
      {"keys":"ctrl+alt+up",    "action":"brightness","target":"@all","value":"-10"},
      {"keys":"ctrl+alt+down",  "action":"brightness","target":"@all","value":"+10"},
      {"keys":"ctrl+alt+left",  "action":"input",     "target":"@all","value":"dp1"},
      {"keys":"ctrl+alt+right", "action":"input",     "target":"@all","value":"hdmi2"},
      {"keys":"ctrl+alt+p",     "action":"power",     "target":"@all","value":"off"}]}

Keys: ctrl shift alt win + up down left right p. Edit file, restart daemon.

Run `bin\monctl-tray.exe watch` at login for silent hotkeys:
`monctl autostart` registers it under HKCU ...\CurrentVersion\Run.

## Layout

    main.go            CLI dispatch
    watch.go           hotkey daemon (golang.design/x/hotkey)
    autostart.go       config + Run-key registration
    monitor/           Controller seam; windows.go = dxva2/user32 syscalls;
                       vcp.go = VCP codes + input map (+ tests)

macOS: implement the same Controller in one new file (CoreDisplay private API
or shelling to ddcctl); hotkey package already supports macOS (needs
Accessibility permission).
