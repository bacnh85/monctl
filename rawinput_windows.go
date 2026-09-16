//go:build windows

// Shared Win32 raw-input plumbing: hidden message window, WM_INPUT pump,
// and HID usage decoding. Used by probe-input and the native brightness
// watcher (consumer page 0x0C: brightness up 0x6F / down 0x70).
package main

import (
	"fmt"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	hidDLL   = windows.NewLazySystemDLL("hid.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procRegisterClassExW        = user32.NewProc("RegisterClassExW")
	procCreateWindowExW         = user32.NewProc("CreateWindowExW")
	procDefWindowProcW          = user32.NewProc("DefWindowProcW")
	procGetMessageW             = user32.NewProc("GetMessageW")
	procSetTimer                = user32.NewProc("SetTimer")
	procPostQuitMessage         = user32.NewProc("PostQuitMessage")
	procRegisterRawInputDevices = user32.NewProc("RegisterRawInputDevices")
	procGetRawInputData         = user32.NewProc("GetRawInputData")
	procGetRawInputDeviceInfoW  = user32.NewProc("GetRawInputDeviceInfoW")
	procGetRawInputDeviceList   = user32.NewProc("GetRawInputDeviceList")
	procSendInput               = user32.NewProc("SendInput")
	procSetWindowsHookExW       = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx     = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx          = user32.NewProc("CallNextHookEx")

	procHidD_GetPreparsedData  = hidDLL.NewProc("HidD_GetPreparsedData")
	procHidD_FreePreparsedData = hidDLL.NewProc("HidD_FreePreparsedData")
	procHidP_GetUsages         = hidDLL.NewProc("HidP_GetUsages")
	procHidP_GetCaps           = hidDLL.NewProc("HidP_GetCaps")
	procHidP_SetUsages         = hidDLL.NewProc("HidP_SetUsages")
)

const (
	ridiPreparsedData = 0x20000005 // RIDI_PREPARSEDDATA
	hidpStatusSuccess = 0x00110000
)

// hidCaps is HIDP_CAPS (fixed USHORT layout, identical on 32/64-bit).
type hidCaps struct {
	Usage, UsagePage          uint16
	InputReportByteLength     uint16
	OutputReportByteLength    uint16
	FeatureReportByteLength   uint16
	Reserved                  [17]uint16
	NumberLinkCollectionNodes uint16
	NumberInputButtonCaps     uint16
	NumberInputValueCaps      uint16
	NumberInputDataIndices    uint16
	NumberOutputDataIndices   uint16
	NumberFeatureDataIndices  uint16
}

// rawDeviceHeader is RAWINPUTHEADER, first member of every RAWINPUT struct.
type rawDeviceHeader2 struct {
	DwType, DwSize uint32
	HDevice        uintptr
	WParam         uintptr
}

// rawDeviceInfo is the RAWINPUTDEVICELIST entry.
type rawDeviceInfo struct {
	HDevice uintptr
	DwType  uint32
}

const (
	wmInput              = 0x00FF
	wmInputDeviceChange  = 0x00FE
	wmTimer              = 0x0113
	gidcArrival          = 0x0001
	gidcRemoval          = 0x0002
	rimTypeHID           = 2
	ridInput             = 0x10000003 // RID_INPUT
	ridiDevName          = 0x20000007 // RIDI_DEVICENAME
	ridevInputSink       = 0x00000100
	ridevDevNotify       = 0x00002000 // WM_INPUT_DEVICE_CHANGE for this collection
	pageConsumer         = 0x0C
	pageKeyboard         = 0x01
	usageConsumerControl = 0x01
	usageKeyboardKeypad  = 0x06
	hidpInput            = 0
)

type wndClassEx struct {
	CbSize, Style               uint32
	LpfnWndProc                 uintptr
	CbClsExtra, CbWndExtra      int32
	HInstance, HIcon, HCursor   uintptr
	HbrBackground, LpszMenuName uintptr
	LpszClassName, HIconSm      *uint16
}

type rawInputDevice struct {
	UsUsagePage, UsUsage uint16
	DwFlags              uint32
	HwndTarget           uintptr
}

type rawInputHeader struct {
	DwType, DwSize uint32
	HDevice        uintptr
	WParam         uintptr
}

// RAWINPUT, HID arm only (header + RAWHID).
type rawInputHID struct {
	Header  rawInputHeader
	SizeHid uint32
	Count   uint32
	RawData [1]byte
}

// RAWKEYBOARD, delivered after the header for RIM_TYPEKEYBOARD events.
type rawKeyboard struct {
	MakeCode, Flags, Reserved, VKey uint16
	Message                         uint32
	ExtraInformation                uint32
}

type msg struct {
	HWnd           uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Pt             struct{ X, Y int32 }
}

func utf16Ptr(s string) *uint16 { p, _ := syscall.UTF16PtrFromString(s); return p }

// createMessageWindow creates a message-only-style hidden window for
// WM_INPUT delivery. Caller must hold the OS thread (LockOSThread).
func createMessageWindow(className string) (uintptr, error) {
	hInst, _, _ := kernel32.NewProc("GetModuleHandleW").Call(0)
	cls := wndClassEx{CbSize: uint32(unsafe.Sizeof(wndClassEx{}))}
	cls.LpfnWndProc = procDefWindowProcW.Addr() // true fn pointer, not a call
	cls.HInstance = hInst
	cls.LpszClassName = utf16Ptr(className)
	if a, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&cls))); a == 0 {
		return 0, fmt.Errorf("RegisterClassExW failed")
	}
	hwnd, _, _ := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls.LpszClassName)),
		uintptr(unsafe.Pointer(utf16Ptr(className))), 0,
		0xFFFFFFFF, 0xFFFFFFFF, 100, 100, 0, 0, hInst, 0)
	if hwnd == 0 {
		return 0, fmt.Errorf("CreateWindowExW failed")
	}
	return hwnd, nil
}

// registerRawInput registers the given collections for the window in one
// call — later RegisterRawInputDevices calls replace earlier ones.
func registerRawInput(hwnd uintptr, devs []rawInputDevice) error {
	r, _, err := procRegisterRawInputDevices.Call(uintptr(unsafe.Pointer(&devs[0])),
		uintptr(len(devs)), uintptr(unsafe.Sizeof(devs[0])))
	if r == 0 {
		return fmt.Errorf("RegisterRawInputDevices: %v", err)
	}
	return nil
}

// pump runs the message loop: WM_INPUT -> onInput(wParam, lParam),
// WM_INPUT_DEVICE_CHANGE -> onDeviceChange(wParam, lParam); after timeout ->
// onTimer (which usually posts quit); WM_QUIT -> returns.
func pump(timeout time.Duration, onInput func(wParam, lParam uintptr),
	onDeviceChange func(wParam, lParam uintptr), onTimer func()) error {
	if timeout > 0 {
		procSetTimer.Call(0, 1, uintptr(timeout.Milliseconds()), 0)
	}
	for {
		var m msg
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(ret) {
		case -1:
			return fmt.Errorf("GetMessage failed")
		case 0: // WM_QUIT
			return nil
		}
		switch m.Message {
		case wmInput:
			onInput(m.WParam, m.LParam)
		case wmInputDeviceChange:
			if onDeviceChange != nil {
				onDeviceChange(m.WParam, m.LParam)
			}
		case wmTimer:
			onTimer()
		}
	}
}

// getRawInputBuffer fetches one RAWINPUT report into a fresh buffer.
func getRawInputBuffer(hRawInput uintptr) []byte {
	var sz uint32
	hdrSize := uint32(unsafe.Sizeof(rawInputHeader{}))
	// Size query: per MSDN the return is 0 on success when pData is NULL.
	r, _, _ := procGetRawInputData.Call(hRawInput, ridInput, 0, uintptr(unsafe.Pointer(&sz)), uintptr(hdrSize))
	if r == 0xFFFFFFFF || sz == 0 {
		return nil
	}
	buf := make([]byte, sz)
	r, _, _ = procGetRawInputData.Call(hRawInput, ridInput, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&sz)), uintptr(hdrSize))
	if r == 0xFFFFFFFF {
		return nil
	}
	return buf
}

// hidReports splits a HID RAWINPUT buffer into its input reports.
func hidReports(buf []byte) (hDevice uintptr, reports [][]byte) {
	if len(buf) < int(unsafe.Sizeof(rawInputHID{})) {
		return 0, nil
	}
	ri := *(*rawInputHID)(unsafe.Pointer(&buf[0]))
	if int(ri.Header.DwType) != rimTypeHID {
		return 0, nil
	}
	dataOff := int(unsafe.Offsetof(ri.RawData))
	for i := uint32(0); i < ri.Count; i++ {
		lo := dataOff + int(i)*int(ri.SizeHid)
		hi := lo + int(ri.SizeHid)
		if lo >= len(buf) || hi > len(buf) {
			break
		}
		reports = append(reports, buf[lo:hi])
	}
	return ri.Header.HDevice, reports
}

// hidUsages returns the usages set in an input report on one usage page.
// Padded copy: HidP_GetUsages validates report length against
// InputReportByteLength, which we don't query; 64 covers common pads.
func hidUsages(hDevice uintptr, report []byte, page uint16) []uint16 {
	var pp uintptr
	if r, _, _ := procHidD_GetPreparsedData.Call(hDevice, uintptr(unsafe.Pointer(&pp))); r == 0 {
		return nil
	}
	defer procHidD_FreePreparsedData.Call(pp)
	padded := make([]byte, 64)
	copy(padded, report)
	list := make([]uint16, 32)
	n := uint32(len(list))
	st, _, _ := procHidP_GetUsages.Call(hidpInput, uintptr(page), 0,
		uintptr(unsafe.Pointer(&list[0])), uintptr(unsafe.Pointer(&n)), pp,
		uintptr(unsafe.Pointer(&padded[0])), uintptr(len(padded)))
	if int32(st) < 0 { // HIDP_STATUS_* error (e.g. page absent on this device)
		return nil
	}
	return list[:n]
}

// listRawDevices returns every raw-input device attached to the system.
func listRawDevices() []rawDeviceInfo {
	var n uint32
	procGetRawInputDeviceList.Call(0, uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Sizeof(rawDeviceInfo{})))
	if n == 0 {
		return nil
	}
	list := make([]rawDeviceInfo, n)
	if r, _, _ := procGetRawInputDeviceList.Call(uintptr(unsafe.Pointer(&list[0])),
		uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Sizeof(rawDeviceInfo{}))); uint32(r) != n {
		return nil
	}
	return list[:n]
}

// devicePreparsedData fetches the HIDP blob for a raw-input device handle
// (works with HidP_* functions). The returned buffer must stay in scope
// for as long as the pp pointer derived from it is used.
func devicePreparsedData(hDevice uintptr) ([]byte, bool) {
	var n uint32
	procGetRawInputDeviceInfoW.Call(hDevice, ridiPreparsedData, 0, uintptr(unsafe.Pointer(&n)))
	if n == 0 {
		return nil, false
	}
	buf := make([]byte, n)
	r, _, _ := procGetRawInputDeviceInfoW.Call(hDevice, ridiPreparsedData,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if r == 0 || r == 0xFFFFFFFF {
		return nil, false
	}
	return buf, true
}

// deviceCaps returns HIDP_CAPS for a preparsed-data blob.
func deviceCaps(pp uintptr) (hidCaps, bool) {
	var caps hidCaps
	st, _, _ := procHidP_GetCaps.Call(pp, uintptr(unsafe.Pointer(&caps)))
	if uint32(st) != hidpStatusSuccess {
		return caps, false
	}
	return caps, true
}

// usageSupported reports whether a button usage exists in the device's
// input report descriptor, via a HidP_SetUsages probe on a zeroed report:
// it fails with USAGE_NOT_FOUND when the descriptor has no such button.
func usageSupported(pp uintptr, reportLen int, page uint16, usage uint16) bool {
	if reportLen <= 0 {
		return false
	}
	list := []uint16{usage}
	n := uint32(len(list))
	report := make([]byte, reportLen)
	st, _, _ := procHidP_SetUsages.Call(hidpInput, uintptr(page), 0,
		uintptr(unsafe.Pointer(&list[0])), uintptr(unsafe.Pointer(&n)), pp,
		uintptr(unsafe.Pointer(&report[0])), uintptr(reportLen))
	return uint32(st) == hidpStatusSuccess
}

func rawDeviceName(hDevice uintptr) string {
	var n uint32
	// Size query: returns 0 on success (required size lands in n), -1 on error.
	procGetRawInputDeviceInfoW.Call(hDevice, ridiDevName, 0, uintptr(unsafe.Pointer(&n)))
	if n == 0 {
		return "device=?"
	}
	b := make([]uint16, n)
	r, _, _ := procGetRawInputDeviceInfoW.Call(hDevice, ridiDevName, uintptr(unsafe.Pointer(&b[0])), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		return "device=?"
	}
	name := string(utf16.Decode(b[:wcslen(b)]))
	if i := lastSlash(name); i > 0 { // keep the VID/PID tail
		name = name[i+1:]
	}
	return "device=" + name
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\\' {
			return i
		}
	}
	return -1
}

func wcslen(b []uint16) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return len(b)
}

// kbdLLHookStruct is KBDLLHOOKSTRUCT (WH_KEYBOARD_LL lParam).
type kbdLLHookStruct struct {
	VkCode, ScanCode, Flags, Time uint32
	DwExtraInfo                   uintptr
}

const (
	whKeyboardLL  = 13
	llkhfExtended = 0x0001 // E0-prefixed scancode
	llkhfInjected = 0x0010 // synthetic (software-injected) input
)

// installKbdHook installs a global low-level keyboard hook on the calling
// thread (which must pump messages). onKey receives key events
// (vk, scancode, extended, injected, down) — down=false for WM_KEYUP/
// WM_SYSKEYUP. Returns an unhook func and an install error (a failed hook
// silently delivers nothing).
func installKbdHook(onKey func(vk, scan uint32, extended, injected, down bool)) (func(), error) {
	cb := syscall.NewCallback(func(nCode int32, wParam uintptr, lParam unsafe.Pointer) uintptr {
		if nCode >= 0 {
			down, key := false, false
			switch wParam {
			case 0x100, 0x104: // WM_KEYDOWN / WM_SYSKEYDOWN
				down, key = true, true
			case 0x101, 0x105: // WM_KEYUP / WM_SYSKEYUP
				key = true
			}
			if key {
				ks := (*kbdLLHookStruct)(lParam)
				onKey(ks.VkCode, ks.ScanCode, ks.Flags&llkhfExtended != 0, ks.Flags&llkhfInjected != 0, down)
			}
		}
		r, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, uintptr(lParam))
		return r
	})
	hHook, _, err := procSetWindowsHookExW.Call(whKeyboardLL, cb, 0, 0)
	if hHook == 0 {
		return func() {}, fmt.Errorf("SetWindowsHookExW failed: %v", err)
	}
	return func() { procUnhookWindowsHookEx.Call(hHook) }, nil
}
