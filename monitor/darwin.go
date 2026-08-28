//go:build darwin

// macOS DDC/CI backend for Apple Silicon via the private IOAVService API.
// Port of the m1ddc algorithm (github.com/waydabber/m1ddc): CoreDisplay
// display info dictionary -> IORegistry walk for the display's
// DCPAVServiceProxy -> IOAVServiceWriteI2C/ReadI2C with the DDC/CI packet
// protocol. Intel Macs are not supported (classic IOI2C DDC is gone there).
package monitor

/*
#cgo LDFLAGS: -framework CoreDisplay -framework CoreGraphics -framework IOKit -framework CoreFoundation

#include <CoreFoundation/CoreFoundation.h>
#include <CoreGraphics/CoreGraphics.h>
#include <IOKit/IOKitLib.h> // registry walk (umbrella IOKit.h is absent in CLT SDKs)
#include <string.h>

// Private CoreDisplay APIs (same ones m1ddc and MonitorControl use).
typedef void *IOAVServiceRef;
extern IOAVServiceRef IOAVServiceCreateWithService(CFAllocatorRef allocator, io_service_t service);
extern IOReturn IOAVServiceReadI2C(IOAVServiceRef service, uint32_t chipAddress, uint32_t offset, void *outputBuffer, uint32_t outputBufferSize);
extern IOReturn IOAVServiceWriteI2C(IOAVServiceRef service, uint32_t chipAddress, uint32_t dataAddress, void *inputBuffer, uint32_t inputBufferSize);
extern CFDictionaryRef CoreDisplay_DisplayCreateInfoDictionary(CGDirectDisplayID display);

#define DDC_CHIP_DEFAULT  0x37
#define DDC_CHIP_MCDP29XX 0xB7

// Boundary helpers pass opaque refs as void* (cgo mangles CF typedefs).
typedef void *cfDictRef;
typedef void *avServiceRef;

static int goOnlineDisplays(uint32_t *ids, int max) {
    CGDirectDisplayID list[16];
    CGDisplayCount n = 0;
    if (max > 16) max = 16;
    if (CGGetOnlineDisplayList((CGDisplayCount)max, list, &n) != kCGErrorSuccess) return -1;
    for (CGDisplayCount i = 0; i < n && i < (CGDisplayCount)max; i++) ids[i] = (uint32_t)list[i];
    return (int)n;
}

static uint32_t goMainDisplayID(void) { return (uint32_t)CGMainDisplayID(); }

static cfDictRef goDisplayInfoDict(uint32_t id) {
    return (cfDictRef)CoreDisplay_DisplayCreateInfoDictionary((CGDirectDisplayID)id);
}

// goDisplayName extracts DisplayAttributes -> ProductAttributes -> ProductName.
static void goDisplayName(cfDictRef dictRef, char *out, int outLen) {
    out[0] = '\0';
    CFDictionaryRef dict = (CFDictionaryRef)dictRef;
    if (!dict) return;
    CFDictionaryRef attrs = (CFDictionaryRef)CFDictionaryGetValue(dict, CFSTR("DisplayAttributes"));
    if (!attrs) return;
    CFDictionaryRef product = (CFDictionaryRef)CFDictionaryGetValue(attrs, CFSTR("ProductAttributes"));
    if (!product) return;
    CFStringRef name = (CFStringRef)CFDictionaryGetValue(product, CFSTR("ProductName"));
    if (!name) return;
    CFStringGetCString(name, out, outLen, kCFStringEncodingUTF8);
}

// goDisplayProductName resolves the panel marketing name the way m1ddc
// does: CoreDisplay info dict -> IODisplayLocation -> IORegistry adapter ->
// recursive search for DisplayAttributes -> ProductAttributes -> ProductName.
static void goDisplayProductName(uint32_t displayID, char *out, int outLen) {
    out[0] = '\0';
    CFDictionaryRef dict = goDisplayInfoDict(displayID);
    if (!dict) return;
    CFTypeRef loc = CFDictionaryGetValue(dict, CFSTR("IODisplayLocation"));
    if (!loc || CFGetTypeID(loc) != CFStringGetTypeID()) { CFRelease(dict); return; }
    io_service_t adapter = IORegistryEntryCopyFromPath(kIOMainPortDefault, (CFStringRef)loc);
    CFRelease(dict);
    if (!adapter) return;
    CFTypeRef attrs = IORegistryEntrySearchCFProperty(adapter, kIOServicePlane, CFSTR("DisplayAttributes"), kCFAllocatorDefault, kIORegistryIterateRecursively);
    IOObjectRelease(adapter);
    if (!attrs || CFGetTypeID(attrs) != CFDictionaryGetTypeID()) { if (attrs) CFRelease(attrs); return; }
    CFDictionaryRef product = (CFDictionaryRef)CFDictionaryGetValue((CFDictionaryRef)attrs, CFSTR("ProductAttributes"));
    CFStringRef name = product ? (CFStringRef)CFDictionaryGetValue(product, CFSTR("ProductName")) : NULL;
    if (name && CFGetTypeID(name) == CFStringGetTypeID()) {
        CFStringGetCString(name, out, outLen, kCFStringEncodingUTF8);
    }
    CFRelease(attrs);
}

// goDisplayTransport mirrors m1ddc's getDisplayDDCTransport: iterate the whole
// IOService plane, match the IOMobileFramebuffer against the display's adapter
// registry entry, then take the next DCPAVServiceProxy (Location == External)
// and wrap it in an IOAVService. *chip gets 0xB7 for MCDP29xx-based DP
// bridges (they route DDC through a different chip address), else 0x37.
static avServiceRef goDisplayTransport(uint32_t displayID, int *chip) {
    *chip = DDC_CHIP_DEFAULT;
    CFDictionaryRef dict = goDisplayInfoDict(displayID);
    if (!dict) return NULL;
    CFTypeRef loc = CFDictionaryGetValue(dict, CFSTR("IODisplayLocation"));
    if (!loc || CFGetTypeID(loc) != CFStringGetTypeID()) { CFRelease(dict); return NULL; }
    io_service_t adapter = IORegistryEntryCopyFromPath(kIOMainPortDefault, (CFStringRef)loc);
    CFRelease(dict);
    if (!adapter) return NULL;

    uint64_t adapterID = 0;
    if (IORegistryEntryGetRegistryEntryID(adapter, &adapterID) != KERN_SUCCESS) {
        IOObjectRelease(adapter);
        return NULL;
    }

    io_iterator_t iter;
    io_registry_entry_t root = IORegistryGetRootEntry(kIOMainPortDefault);
    if (IORegistryEntryCreateIterator(root, kIOServicePlane, kIORegistryIterateRecursively, &iter) != KERN_SUCCESS) {
        IOObjectRelease(adapter);
        return NULL;
    }
    IOObjectRelease(adapter);

    io_service_t service;
    Boolean fbMatches = false;
    IOAVServiceRef result = NULL;
    while ((service = IOIteratorNext(iter)) != MACH_PORT_NULL) {
        if (IOObjectConformsTo(service, "IOMobileFramebuffer")) {
            uint64_t fbID = 0;
            fbMatches = IORegistryEntryGetRegistryEntryID(service, &fbID) == KERN_SUCCESS && fbID == adapterID;
            IOObjectRelease(service);
            continue;
        }
        io_name_t name;
        IORegistryEntryGetName(service, name);
        if (!fbMatches || strcmp(name, "DCPAVServiceProxy") != 0) {
            IOObjectRelease(service);
            continue;
        }
        CFStringRef location = IORegistryEntrySearchCFProperty(service, kIOServicePlane, CFSTR("Location"), kCFAllocatorDefault, kIORegistryIterateRecursively);
        Boolean isExternal = location != NULL &&
            CFGetTypeID(location) == CFStringGetTypeID() &&
            CFStringCompare(location, CFSTR("External"), 0) == kCFCompareEqualTo;
        if (location) CFRelease(location);
        if (!isExternal) {
            IOObjectRelease(service);
            continue;
        }
        io_registry_entry_t parent = MACH_PORT_NULL;
        if (IORegistryEntryGetParentEntry(service, kIOServicePlane, &parent) == KERN_SUCCESS) {
            CFTypeRef cls = IORegistryEntryCreateCFProperty(parent, CFSTR("EPICProviderClass"), kCFAllocatorDefault, 0);
            if (cls) {
                if (CFGetTypeID(cls) == CFStringGetTypeID() &&
                    CFStringCompare((CFStringRef)cls, CFSTR("AppleDCPMCDP29XX"), 0) == kCFCompareEqualTo) {
                    *chip = DDC_CHIP_MCDP29XX;
                }
                CFRelease(cls);
            }
            IOObjectRelease(parent);
        }
        result = IOAVServiceCreateWithService(kCFAllocatorDefault, service);
        IOObjectRelease(service);
        break;
    }
    IOObjectRelease(iter);
    return result;
}

static IOReturn goDDCWrite(avServiceRef svc, uint32_t chip, uint32_t addr, void *data, uint32_t len) {
    return IOAVServiceWriteI2C((IOAVServiceRef)svc, chip, addr, data, len);
}

static IOReturn goDDCRead(avServiceRef svc, uint32_t chip, uint32_t addr, void *out, uint32_t len) {
    return IOAVServiceReadI2C((IOAVServiceRef)svc, chip, addr, out, len);
}
*/
import "C"

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

const (
	ddcRetries = 3 // same tolerance as the Windows backend

	ddcIterations = 1 // FreeDisplay sends each request once; doubling (m1ddc) clobbers some Samsung panels

	ddcAddrStd = 0x51 // DDC sub-address for all standard VCP codes
	ddcAddrAlt = 0x50 // sub-address for VCP 0xF4 (alternate input select); some Samsung panels use 0x50 exclusively

	ddcChipDefault  = 0x37
	ddcChipMCDP     = 0xB7
	ddcWait         = 40 * time.Millisecond // reply wait (DDC/CI spec ~40ms; FreeDisplay uses this)
	ddcReadWaitMCDP = 50 * time.Millisecond // MCDP29xx returns empty replies at 10ms
)

// ddcDelayOverride returns a MONCTL_DDC_DELAY_MS read-wait override, or 0.
func ddcDelayOverride() time.Duration {
	ms, err := strconv.Atoi(os.Getenv("MONCTL_DDC_DELAY_MS"))
	if err != nil || ms <= 0 || ms > 5000 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

var errDDCHintDarwin = errors.New("(check monitor OSD: System -> DDC/CI Enabled)")

func newPlatform() Controller { return &darwinController{} }

type darwinController struct{}

func (c *darwinController) List() ([]Info, error) {
	ids := make([]C.uint32_t, 16)
	n := C.goOnlineDisplays(&ids[0], 16)
	if n < 0 {
		return nil, errors.New("CGGetOnlineDisplayList failed")
	}
	mainID := C.goMainDisplayID()
	var infos []Info
	for i := 0; i < int(n); i++ {
		id := uint32(ids[i])
		dict := C.goDisplayInfoDict(C.uint32_t(id))
		if dict == nil {
			continue // virtual display (Sidecar/AirPlay): no info dictionary
		}
		C.CFRelease(C.CFTypeRef(unsafe.Pointer(dict)))
		var name [128]C.char
		C.goDisplayProductName(C.uint32_t(id), &name[0], C.int(len(name)))
		desc := C.GoString(&name[0])
		if desc == "" {
			desc = fmt.Sprintf("Display 0x%X", id)
		}
		infos = append(infos, Info{
			Index:       len(infos),
			Description: desc,
			Primary:     id == uint32(mainID),
			Handle:      uintptr(id),
		})
	}
	SortInfos(infos)
	return infos, nil
}

// openTransport resolves the display's IOAVService + chip address.
// Opened per call; safe across display sleep/reconnect.
func openTransport(displayID uint32) (C.avServiceRef, int, error) {
	var chip C.int
	svc := C.goDisplayTransport(C.uint32_t(displayID), &chip)
	if svc == nil {
		return nil, 0, fmt.Errorf("no DDC transport for display %d %w", displayID, errDDCHintDarwin)
	}
	return svc, int(chip), nil
}

func readWait(chip int) time.Duration {
	if chip == ddcChipMCDP {
		return ddcReadWaitMCDP
	}
	return ddcWait
}

// ddcSubAddress returns the DDC/CI sub-address: MONCTL_DDC_ADDR hex override
// when set (some panels, e.g. the G95NC on DP, answer on 0x50 not 0x51),
// else the standard 0x51. override=true means the env value wins for ALL
// codes (including 0xF4). err when the override is not valid hex.
func ddcSubAddress() (addr byte, override bool, err error) {
	raw := os.Getenv("MONCTL_DDC_ADDR")
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return ddcAddrStd, false, nil
	}
	n, perr := strconv.ParseUint(strings.TrimPrefix(v, "0x"), 16, 8)
	if perr != nil {
		return 0, false, fmt.Errorf("bad MONCTL_DDC_ADDR %q (hex, e.g. 50)", raw)
	}
	return byte(n), true, nil
}

// ddcAttemptAddr picks the sub-address for one retry attempt: env override
// always wins; otherwise alternate standard 0x51 / Samsung 0x50 across
// attempts (0xF4 alternate-input always uses 0x50).
func ddcAttemptAddr(code byte, override bool, addr byte, attempt int) byte {
	if override {
		return addr
	}
	if code == 0xF4 {
		return ddcAddrAlt
	}
	if attempt%2 == 1 {
		return ddcAddrAlt // 0x50: Samsung G95NC (DP) answers here
	}
	return ddcAddrStd
}

// GetVCP sends a Get VCP Feature request and reads the reply.
// Per m1ddc, every DDC write is issued twice (DDC_ITERATIONS=2): the first
// transaction wakes the panel's DDC path, the second carries the request.
// The reply is validated (source byte + echoed VCP code) before acceptance;
// Samsung panels otherwise serve stale frames from earlier transactions.
func (c *darwinController) GetVCP(info Info, code byte) (int, int, error) {
	svc, chip, err := openTransport(uint32(info.Handle))
	if err != nil {
		return 0, 0, err
	}
	defer C.CFRelease(C.CFTypeRef(unsafe.Pointer(svc)))

	srcOverride, override, serr := ddcSubAddress()
	if serr != nil {
		return 0, 0, serr
	}
	for attempt := 0; ; attempt++ {
		// Panels disagree on the DDC sub-address (0x51 standard, 0x50 on e.g.
		// G95NC/DP); alternate per attempt, env override wins. Checksum covers
		// src, so the request is rebuilt each attempt.
		src := ddcAttemptAddr(code, override, srcOverride, attempt)
		req := ddcReadReq(code, src)
		delay := readWait(chip)
		if ovr := ddcDelayOverride(); ovr != 0 {
			delay = ovr
		}
		for i := 0; i < ddcIterations; i++ {
			if rv := C.goDDCWrite(svc, C.uint32_t(chip), C.uint32_t(src), unsafe.Pointer(&req[0]), C.uint32_t(len(req))); rv != 0 {
				break
			}
		}
		time.Sleep(delay)
		var buf [12]byte
		if C.goDDCRead(svc, C.uint32_t(chip), C.uint32_t(src), unsafe.Pointer(&buf[0]), C.uint32_t(len(buf))) == 0 {
			if os.Getenv("MONCTL_DEBUG") != "" {
				fmt.Fprintf(os.Stderr, "monctl: vcp 0x%02X src=0x%02X reply: %s\n", code, src, hex.EncodeToString(buf[:]))
			}
			if ddcReplyValid(buf[:], code) {
				cur, max, _ := parseDDCReply(buf[:])
				return cur, max, nil
			}
		}
		if attempt == ddcRetries-1 {
			return 0, 0, fmt.Errorf("GetVCP(0x%02X): DDC read failed %w", code, errDDCHintDarwin)
		}
	}
}

// SetVCP sends a Set VCP Feature request.
func (c *darwinController) SetVCP(info Info, code byte, value byte) error {
	svc, chip, err := openTransport(uint32(info.Handle))
	if err != nil {
		return err
	}
	defer C.CFRelease(C.CFTypeRef(unsafe.Pointer(svc)))

	srcOverride, override, serr := ddcSubAddress()
	if serr != nil {
		return serr
	}
	for attempt := 0; ; attempt++ {
		// Alternate sub-address per attempt (checksum covers src, so the
		// packet must be rebuilt each attempt). A write-only op cannot verify
		// which address the panel honors; try 0x51 then 0x50.
		src := ddcAttemptAddr(code, override, srcOverride, attempt)
		req := ddcWriteReq(code, src, uint16(value))
		time.Sleep(ddcWait)
		ok := true
		for i := 0; i < ddcIterations; i++ {
			if rv := C.goDDCWrite(svc, C.uint32_t(chip), C.uint32_t(src), unsafe.Pointer(&req[0]), C.uint32_t(len(req))); rv != 0 {
				ok = false
				break
			}
		}
		if ok {
			return nil
		}
		if attempt == ddcRetries-1 {
			return fmt.Errorf("SetVCP(0x%02X, 0x%02X): DDC write failed %w", code, value, errDDCHintDarwin)
		}
	}
}

// ---- Pure-Go packet helpers (unit-tested in darwin_test.go) ----

// ddcReadReq builds the 4-byte DDC/CI "Get VCP Feature" request:
// 0x82 0x01 <code> <cksum>, cksum = 0x6E ^ src ^ 0x82 ^ 0x01 ^ code.
// NOTE: the sub-address IS folded in (DDC/CI spec; FreeDisplay does this).
// m1ddc omits it — lenient panels tolerate that, but e.g. the G95NC
// silently discards get-requests with a bad checksum.
func ddcReadReq(code, src byte) [4]byte {
	b := [4]byte{0x82, 0x01, code}
	b[3] = 0x6E ^ src ^ b[0] ^ b[1] ^ b[2]
	return b
}

// ddcWriteReq builds the 6-byte DDC/CI "Set VCP Feature" request:
// 0x84 0x03 <code> <hi> <lo> <cksum>, cksum = 0x6E ^ src ^ b[0..4]
// (m1ddc's prepareDDCWrite: unlike read, the source address IS included).
func ddcWriteReq(code, src byte, value uint16) [6]byte {
	b := [6]byte{0x84, 0x03, code, byte(value >> 8), byte(value)}
	b[5] = 0x6E ^ src ^ b[0] ^ b[1] ^ b[2] ^ b[3] ^ b[4]
	return b
}

// parseDDCReply extracts current/max from a Get VCP Feature Reply:
// max = big-endian bytes 6..7, current = big-endian bytes 8..9.
func parseDDCReply(buf []byte) (cur, max int, ok bool) {
	if len(buf) < 10 {
		return 0, 0, false
	}
	max = int(binary.BigEndian.Uint16(buf[6:8]))
	cur = int(binary.BigEndian.Uint16(buf[8:10]))
	return cur, max, cur != 0 || max != 0
}

// ddcReplyValid validates a Get VCP Feature Reply: source byte 0x6E at [0],
// the requested VCP code echoed at [4], and a plausible payload (m1ddc does
// no validation but always writes the request twice before reading; Samsung
// panels otherwise serve stale frames from earlier transactions).
func ddcReplyValid(buf []byte, code byte) bool {
	if len(buf) < 10 || buf[0] != 0x6E || buf[2] != 0x02 || buf[4] != code {
		return false
	}
	zeroPayload := buf[6] == 0 && buf[7] == 0 && buf[8] == 0 && buf[9] == 0
	// Power mode (0xD6): some panels (G95NC) acknowledge the request but
	// report 0/0 — accept it rather than failing the whole read.
	if zeroPayload && code != 0xD6 {
		return false
	}
	return true
}
