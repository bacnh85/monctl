//go:build windows

// Windows DDC/CI backend over dxva2.dll / user32.dll syscalls.
package monitor

import (
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	dxva2  = windows.NewLazySystemDLL("dxva2.dll")
	user32 = windows.NewLazySystemDLL("user32.dll")

	procGetNumPhysicalMonitors = dxva2.NewProc("GetNumberOfPhysicalMonitorsFromHMONITOR")
	procGetPhysicalMonitors    = dxva2.NewProc("GetPhysicalMonitorsFromHMONITOR")
	procDestroyPhysicalMonitor = dxva2.NewProc("DestroyPhysicalMonitor")
	procGetVCPFeature          = dxva2.NewProc("GetVCPFeatureAndVCPFeatureReply")
	procSetVCPFeature          = dxva2.NewProc("SetVCPFeature")

	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfo      = user32.NewProc("GetMonitorInfoW")
	procEnumDisplayDevices  = user32.NewProc("EnumDisplayDevicesW")
)

// physicalMonitor mirrors Win32 PHYSICAL_MONITOR:
// HANDLE hPhysicalMonitor; WCHAR szPhysicalMonitorDescription[128];
// -> 8 + 256 bytes on amd64. Must stay mirrored exactly (struct layout is ABI).
type physicalMonitor struct {
	Handle      syscall.Handle
	Description [128]uint16
}

// monitorInfoEx mirrors Win32 MONITORINFOEXW (104 bytes on amd64):
// cbSize(4) rcMonitor(16) rcWork(16) dwFlags(4) szDevice[32]WCHAR(64).
type monitorInfoEx struct {
	CbSize    uint32
	RcMonitor rect32
	RcWork    rect32
	DwFlags   uint32
	SzDevice  [32]uint16
}

type rect32 struct{ Left, Top, Right, Bottom int32 }

const (
	monitorInfoPrimary = 0x00000001 // MONITORINFOF_PRIMARY
	ddcRetries         = 3
	ddcRetryDelay      = 50 * time.Millisecond
)

// errDDCHint wraps transient/hardware failures with the OSD hint.
var errDDCHint = fmt.Errorf("(check monitor OSD: System -> DDC/CI Enabled)")

func newPlatform() Controller { return &windowsController{} }

type windowsController struct{}

// List enumerates display monitors via EnumDisplayMonitors.
func (c *windowsController) List() ([]Info, error) {
	var infos []Info
	cb := syscall.NewCallback(func(hmon, hdc, lprect, lParam uintptr) uintptr {
		desc, primary := monitorDescription(hmon)
		infos = append(infos, Info{
			Index:       len(infos),
			Description: desc,
			Primary:     primary,
			Handle:      hmon,
		})
		return 1 // TRUE = continue enumeration
	})
	ret, _, callErr := procEnumDisplayMonitors.Call(0, 0, cb, 0)
	if ret == 0 {
		return nil, fmt.Errorf("EnumDisplayMonitors failed: %v", callErr)
	}
	SortInfos(infos)
	// Prefer the EDID identity (e.g. "SAM Odyssey Odyssey Arc G95NC"), then
	// the DDC description, then the GDI adapter path.
	for i := range infos {
		if d := modelForAdapter(infos[i].Description); d != "" {
			infos[i].Description = d + " (EDID)"
		} else if d := physicalDescription(infos[i]); d != "" {
			infos[i].Description = d
		}
	}
	return infos, nil
}

// physicalDescription opens the physical monitor just to read its description.
func physicalDescription(info Info) string {
	pm, closeFn, err := openPhysical(info.Handle)
	if err != nil {
		return ""
	}
	defer closeFn()
	desc := syscall.UTF16ToString(pm.Description[:])
	return strings.TrimSpace(desc)
}

func monitorDescription(hmon uintptr) (desc string, primary bool) {
	var mi monitorInfoEx
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	ret, _, _ := procGetMonitorInfo.Call(hmon, uintptr(unsafe.Pointer(&mi)))
	if ret == 0 {
		return "", false
	}
	return syscall.UTF16ToString(mi.SzDevice[:]), mi.DwFlags&monitorInfoPrimary != 0
}

// openPhysical acquires the first physical monitor behind an HMONITOR.
// Caller must closePhysical when done — leaked handles wedge later DDC calls.
func openPhysical(hmon uintptr) (*physicalMonitor, func(), error) {
	var count uint32
	ret, _, callErr := procGetNumPhysicalMonitors.Call(hmon, uintptr(unsafe.Pointer(&count)))
	if ret == 0 || count == 0 {
		return nil, nil, fmt.Errorf("GetNumberOfPhysicalMonitorsFromHMONITOR: %v %w", callErr, errDDCHint)
	}
	mons := make([]physicalMonitor, count)
	ret, _, callErr = procGetPhysicalMonitors.Call(hmon, uintptr(count), uintptr(unsafe.Pointer(&mons[0])))
	if ret == 0 {
		return nil, nil, fmt.Errorf("GetPhysicalMonitorsFromHMONITOR: %v %w", callErr, errDDCHint)
	}
	pm := &mons[0]
	closeFn := func() { _, _, _ = procDestroyPhysicalMonitor.Call(uintptr(pm.Handle)) }
	return pm, closeFn, nil
}

// GetVCP reads current/max for a VCP code. DDC reads are flaky over HDMI;
// retry a few times before giving up.
func (c *windowsController) GetVCP(info Info, code byte) (current, max int, err error) {
	pm, closeFn, err := openPhysical(info.Handle)
	if err != nil {
		return 0, 0, err
	}
	defer closeFn()

	var (
		curVal, maxVal uint32
		vcpType        int32 // MC_VCP_CODE_TYPE
	)
	for attempt := 0; ; attempt++ {
		ret, _, callErr := procGetVCPFeature.Call(
			uintptr(pm.Handle),
			uintptr(code),
			uintptr(unsafe.Pointer(&vcpType)),
			uintptr(unsafe.Pointer(&curVal)),
			uintptr(unsafe.Pointer(&maxVal)),
		)
		if ret != 0 {
			return int(curVal), int(maxVal), nil
		}
		lastErr := fmt.Errorf("GetVCPFeatureAndVCPFeatureReply(0x%02X): %v %w", code, callErr, errDDCHint)
		if attempt == ddcRetries-1 {
			return 0, 0, lastErr
		}
		time.Sleep(ddcRetryDelay)
	}
}

// SetVCP writes a VCP code value, with the same retry tolerance as GetVCP.
func (c *windowsController) SetVCP(info Info, code byte, value byte) error {
	pm, closeFn, err := openPhysical(info.Handle)
	if err != nil {
		return err
	}
	defer closeFn()

	for attempt := 0; ; attempt++ {
		ret, _, callErr := procSetVCPFeature.Call(
			uintptr(pm.Handle),
			uintptr(code),
			uintptr(value),
		)
		if ret != 0 {
			return nil
		}
		lastErr := fmt.Errorf("SetVCPFeature(0x%02X, 0x%02X): %v %w", code, value, callErr, errDDCHint)
		if attempt == ddcRetries-1 {
			return lastErr
		}
		time.Sleep(ddcRetryDelay)
	}
}

// ---- Monitor identity via EnumDisplayDevices + registry EDID ----

// displayDeviceW mirrors Win32 DISPLAY_DEVICEW.
type displayDeviceW struct {
	Cb           uint32
	DeviceName   [32]uint16
	DeviceString [128]uint16
	StateFlags   uint32
	DeviceID     [128]uint16
	DeviceKey    [128]uint16
}

const eddGetDeviceInterfaceName = 0x1

// modelForAdapter resolves the EDID identity (e.g. "SAMSUNG Odyssey ...")
// for a GDI adapter path like \\.\DISPLAY1. Empty string if unresolved.
func modelForAdapter(gdi string) string {
	if !strings.HasPrefix(gdi, `\\.\`) {
		return ""
	}
	// Enumerate this adapter's attached monitor device (e.g.
	// DISPLAY1\Monitor0) carrying the hardware ID we need for EDID lookup.
	for i := uint32(0); ; i++ {
		var d displayDeviceW
		d.Cb = uint32(unsafe.Sizeof(d))
		ptr, _ := windows.UTF16PtrFromString(gdi)
		r1, _, _ := procEnumDisplayDevices.Call(
			uintptr(unsafe.Pointer(ptr)), uintptr(i), uintptr(unsafe.Pointer(&d)), 0)
		if r1 == 0 {
			break
		}
		if d.StateFlags&0x1 == 0 { // DISPLAY_DEVICE_ACTIVE
			continue
		}
		deviceID := syscall.UTF16ToString(d.DeviceID[:])
		if m := edidModel(deviceID); m != "" {
			return m
		}
	}
	return ""
}

// edidModel walks HKLM\SYSTEM\CurrentControlSet\Enum\<deviceID> reading the
// EDID blob and extracting the model name (descriptor type 0xFC) + PNP id.
// deviceID arrives as e.g. MONITOR\SAM73D0\{4d36e96e-...}\0003 — that last
// component is an instance *index*, the registry instance key differs, so we
// match on the HW id (2nd component) instead.
func edidModel(deviceID string) string {
	parts := strings.Split(deviceID, `\`)
	if len(parts) < 2 {
		return ""
	}
	hwID := parts[1] // e.g. SAM73D0
	enumBase := `SYSTEM\CurrentControlSet\Enum\DISPLAY\` + hwID
	keys, err := registry.OpenKey(registry.LOCAL_MACHINE, enumBase, registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return ""
	}
	defer keys.Close()
	names, err := keys.ReadSubKeyNames(-1)
	if err != nil {
		return ""
	}
	for _, inst := range names {
		key, err := registry.OpenKey(registry.LOCAL_MACHINE,
			enumBase+`\`+inst+`\Device Parameters`, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		blob := make([]byte, 256) // EDID is 128/256 bytes
		n, _, err := key.GetValue("EDID", blob)
		key.Close()
		if err != nil || n < 54 {
			continue
		}
		if m := parseEDIDModel(blob[:n]); m != "" {
			return m
		}
	}
	return ""
}

// parseEDIDModel extracts the model name from a 128+ byte EDID:
// four 18-byte descriptors at offset 54; 0xFC = monitor name text (bytes 5..17).
// This G95NC writes the type byte at index 3 (00 00 00 FC 00 + text) instead of
// the spec position 2 — accept both (real-world panel quirk).
func parseEDIDModel(edid []byte) string {
	for off := 54; off+18 <= len(edid); off += 18 {
		d := edid[off : off+18]
		if d[0] != 0 || d[1] != 0 || (d[2] != 0xFC && d[3] != 0xFC) {
			continue
		}
		name := strings.TrimRight(string(d[5:]), "\x00\n ")
		if name != "" {
			return strings.TrimSpace(name)
		}
	}
	return ""
}
