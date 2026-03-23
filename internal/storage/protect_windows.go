//go:build windows

package storage

import (
	"fmt"
	"syscall"
	"unsafe"
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

var (
	crypt32                = syscall.NewLazyDLL("Crypt32.dll")
	kernel32               = syscall.NewLazyDLL("Kernel32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	procLocalFree          = kernel32.NewProc("LocalFree")
)

const cryptProtectUIForbidden = 0x1

func encrypt(data []byte) ([]byte, error) {
	return cryptProtect(data)
}

func decrypt(data []byte) ([]byte, error) {
	return cryptUnprotect(data)
}

func cryptProtect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return []byte{}, nil
	}
	in := dataBlob{cbData: uint32(len(data)), pbData: &data[0]}
	var out dataBlob
	r, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		0,
		0,
		0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptProtectData: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	buf := unsafe.Slice(out.pbData, out.cbData)
	return append([]byte(nil), buf...), nil
}

func cryptUnprotect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return []byte{}, nil
	}
	in := dataBlob{cbData: uint32(len(data)), pbData: &data[0]}
	var out dataBlob
	r, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		0,
		0,
		0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("CryptUnprotectData: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	buf := unsafe.Slice(out.pbData, out.cbData)
	return append([]byte(nil), buf...), nil
}
