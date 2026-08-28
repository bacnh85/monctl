# monctl

DDC/CI monitor control CLI for Windows and macOS (Apple Silicon). Single Go
binary — scan, control brightness/contrast/input/power, bind global hotkeys.

Verified live on:
- Windows: Samsung Odyssey G95NC via HDMI-2 (input `0x12`, brightness VCP max=50)
- macOS (M4): Samsung Odyssey G95NC via DisplayPort — brightness/contrast/input/power-set
  all working. Note: stock `m1ddc` cannot READ from this panel (it omits the
  sub-address from the get-request checksum, which the G95NC silently rejects);
  monctl sends spec-correct checksums and self-adapts the sub-address per retry.

## Build

    go build -o bin/monctl.exe .                                  # Windows
    go build -ldflags "-H windowsgui" -o bin/monctl-tray.exe .    # Windows silent bg
    go build -o bin/monctl .                                      # macOS (needs CGO + Xcode CLT)

## Usage

    monctl scan                        # list monitors (EDID model) + input/power
    monctl get  brightness|contrast|input|power [-m N]
    monctl set  <name> <value> [-m N]  # value: percent, +N/-N offset, name, or 0xNN
    monctl vcp  <0xcode> [value]       # raw DDC escape hatch
    monctl watch [--config PATH]       # hotkey daemon (foreground)
    monctl config                      # write example config, print path
    monctl autostart [--remove]        # Windows: HKCU Run key; macOS: LaunchAgent

Brightness/contrast values are **percent 0-100**, scaled to the panel-reported
VCP max (G95NC reports 50). Inputs: vga dvi dp1 dp2 hdmi1 hdmi2 usb-c.
Power: on off standby. Requires monitor OSD setting **DDC/CI: Enabled**.

### macOS notes (Apple Silicon)

- Uses the private `IOAVService` DDC/CI API (same as m1ddc / MonitorControl).
  Intel Macs are not supported.
- Global hotkeys (`watch`) need two macOS pieces: the process must run on
  the OS main thread (handled internally via mainthread.Init) and the binary
  must be granted **Accessibility** (System Settings -> Privacy & Security
  -> Accessibility -> add monctl). Without the grant, `watch` exits with a
  clear "grant the application Accessibility" error (verified: with the
  grant, all hotkeys register and the daemon stays up). Note the grant is
  **per binary path**: a rebuilt binary at the same path stays trusted, but
  a copy at a new path needs its own grant.
- Install the binary on the **internal disk** (e.g. `~/bin/monctl`), not an
  external volume — launchd-spawned processes can stall in dyld opening
  binaries on external drives.
- `monctl autostart` installs `~/Library/LaunchAgents/com.bacnh85.monctl.plist`
  (starts `monctl watch` at login, KeepAlive).
- Panels disagree on the DDC sub-address; monctl alternates 0x51 (standard)
  and 0x50 (some Samsungs) per retry. Force one with `MONCTL_DDC_ADDR=50`;
  tune the reply wait with `MONCTL_DDC_DELAY_MS` (default 40ms per spec).
- `MONCTL_DEBUG=1` dumps raw DDC reply frames to stderr.
- Quit DDC-polling apps (BetterDisplay, Lunar, MonitorControl) when debugging
  DDC — concurrent bus access corrupts reads.
- G95NC quirks: `get power` returns 0/0 (the panel accepts power-mode writes
  but does not report the state back); `set power off/standby` works.

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

### Native F1/F2 brightness keys (macOS)

On macOS `monctl watch` also intercepts the physical F1/F2 brightness keys
and routes them to DDC brightness on all monitors. Both key forms are
tapped: the NX brightness media events AND the plain F1/F2 keycodes, so it
works regardless of keyboard mode. On backlight-less Macs (Mac mini) macOS
never synthesizes brightness media events, so there you use **Fn+F1/F2**
(or enable "Use F1, F2 as standard function keys" in Keyboard settings for
bare F1/F2). Add `"native_brightness": false` to config.json to opt out.

Run `bin\monctl-tray.exe watch` at login for silent hotkeys:
`monctl autostart` registers it under HKCU ...\CurrentVersion\Run.

## Layout

    main.go            CLI dispatch
    watch.go           hotkey daemon (golang.design/x/hotkey)
    autostart*.go      config + autostart registration (Run key / LaunchAgent)
    monitor/           Controller seam; windows.go = dxva2/user32 syscalls;
                       darwin.go = IOAVService DDC (Apple Silicon, cgo);
                       vcp.go = VCP codes + input map (+ tests)
