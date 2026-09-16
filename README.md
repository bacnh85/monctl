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

## Install (macOS)

Download `monctl-macos-arm64.dmg` from the [latest release](../../releases/latest), open it,
and drag **monctl** to **Applications**.

Then in Terminal:

```bash
/Applications/monctl.app/Contents/MacOS/monctl autostart   # installs LaunchAgent
```

Grant Accessibility when prompted (System Settings -> Privacy & Security ->
Accessibility -> monctl). The daemon starts automatically at login and
registers the hotkeys (including the native brightness-key tap).

> The .app is ad-hoc signed (no Apple Developer ID), so macOS may ask you to
> approve it under Privacy & Security on first run — click "Open Anyway".
> For the CLI alone you can also `brew install go && go install
> github.com/bacnh85/monctl@latest` and run `monctl` directly (no hotkeys
> or autostart).

## Install (Windows)

Download `monctl-windows-amd64.exe` (and optionally `monctl-tray-…` for the
silent variant), then:

```powershell
monctl autostart    # registers HKCU Run key
```

## Hotkeys

`monctl watch` reads `%APPDATA%\monctl\config.json` (written by `monctl config`):

    {"hotkeys":[
      {"keys":"ctrl+alt+up",    "action":"brightness","target":"@all","value":"-10"},
      {"keys":"ctrl+alt+down",  "action":"brightness","target":"@all","value":"+10"},
      {"keys":"ctrl+alt+left",  "action":"input",     "target":"@all","value":"dp1"},
      {"keys":"ctrl+alt+right", "action":"input",     "target":"@all","value":"hdmi2"},
      {"keys":"ctrl+alt+shift+p","action":"power",     "target":"@all","value":"off"}]}

Keys: ctrl shift alt win + up down left right p. Edit file, restart daemon.

### Stuck Ctrl/Alt/Tab after KVM switches (Windows)

Switching the monitor's input also moves its USB hub between hosts. A host
that loses the keyboard mid-keystroke never sees the key-ups; the host that
gains it can receive phantom reports at enumeration (and Logitech G HUB /
Options+ re-inject synthetic input when the device (re)appears — AltGr/RAlt
presents as BOTH Ctrl and Alt stuck). `monctl watch` on Windows therefore
scrubs modifier + Tab key state via `WM_INPUT_DEVICE_CHANGE`: three passes
on keyboard arrival (0/1s/3s — covers agent re-injection), one on removal.
This covers OSD-menu switches too, which bypass monctl entirely. It runs
even with `"native_brightness": false` (the raw-input listener is kept),
is single-flight (multi-TLC keyboards fire one sequence, not several), and
skips only keys PHYSICALLY pressed on this host — software re-injections
(G HUB/Options+) are tracked as phantoms, never as held keys, so they stay
scrub-eligible.
`monctl probe-input -t 30s` during a switch shows which mechanism produces
the phantoms on your setup (`injected=true` events = G HUB/Options+, not
the keyboard). macOS scrubbing (Windows→Mac direction) is not implemented.

### Native brightness keys (macOS, Fn layer / media events)

On macOS `monctl watch` taps the **brightness media events** (NX_KEYTYPE_
BRIGHTNESS_UP/DOWN) and routes them to DDC brightness on all monitors —
bare F1/F2 keycodes are deliberately NOT touched and keep working as
normal function keys for apps.

Backlight-less Macs (Mac mini) asymmetry, observed live: macOS synthesizes
BRIGHTNESS_DOWN media events but silently swallows BRIGHTNESS_UP (nothing
internal to brighten). With a vendor keyboard (Logitech etc.) whose Fn
layer reaches macOS, Fn+brightness-down works natively; brightness-up has
no native event. The shipped config therefore uses **^⌘F1 = down** (works;
^⌘F2 collides with the macOS Ctrl-F2 menu-bar shortcut and never reaches
taps) and **^⌥F2 = up** (bypasses that collision; also reachable via
Logi Options+ keystroke mapping from the keyboard's brightness keys).
Add `"native_brightness": false` to config.json to opt out of the media
tap.

### Native brightness keys (Windows, raw input)

On Windows `monctl watch` listens on the **HID consumer page** via raw
input (`RIDEV_INPUTSINK`, works in background) and routes the brightness
usages (0x6F up / 0x70 down) to DDC brightness ±10 on all monitors. These
keys are not VK codes, so they cannot be grabbed with RegisterHotKey.

**Logitech Options+ / G HUB caveat:** these apps intercept media keys
*below* the OS input stack (raw input never sees the real reports) and
re-inject only volume/mute as synthetic keystrokes — brightness keys get
dropped into a "system brightness" call that does nothing on DDC-only
monitors. With the Options+ agent (`logioptionsplus_agent.exe`) not
running, the keyboard's native HID usages reach the watcher directly.

Diagnostics: `monctl probe-input -t 30s` logs HID usages + raw keyboard
events + a low-level keyboard hook (with `injected=` flags that expose
software re-injection); `monctl probe-input -list` enumerates which HID
collections support brightness usages without pressing anything.

If you must keep Options+ running: remap the brightness keys *inside*
Options+ to send **F13**/**F14** keystrokes (no physical keyboard emits
those) and bind `"f13"`/`"f14"` in config.json hotkeys. Same
`"native_brightness": false` opt-out applies to the native watcher.

Run `bin\monctl-tray.exe watch` at login for silent hotkeys:
`monctl autostart` registers it under HKCU ...\CurrentVersion\Run.

## Layout

    main.go            CLI dispatch
    watch.go           hotkey daemon (golang.design/x/hotkey)
    autostart*.go      config + autostart registration (Run key / LaunchAgent)
    monitor/           Controller seam; windows.go = dxva2/user32 syscalls;
                       darwin.go = IOAVService DDC (Apple Silicon, cgo);
                       vcp.go = VCP codes + input map (+ tests)
