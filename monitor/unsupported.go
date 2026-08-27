//go:build !windows

// Stub backend for platforms without a DDC/CI implementation yet
// (macOS planned: CoreDisplay private API or ddcctl shell-out).
package monitor

import ()

func newPlatform() Controller { return &unsupported{} }

type unsupported struct{}

func (unsupported) List() ([]Info, error) {
	return nil, ErrNotImplemented
}

func (unsupported) GetVCP(info Info, code byte) (int, int, error) {
	return 0, 0, ErrNotImplemented
}

func (unsupported) SetVCP(info Info, code byte, value byte) error {
	return ErrNotImplemented
}

// Compile-time interface check.
var _ Controller = unsupported{}
