package main

import (
	"os"
	"strings"
	"syscall"
	"unsafe"
)

func detectLocale() string {
	// Try env vars first (Git Bash, WSL, etc.)
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(key); v != "" {
			v, _, _ = strings.Cut(v, ".")
			v, _, _ = strings.Cut(v, "@")
			return strings.ReplaceAll(v, "_", "-")
		}
	}
	// Fall back to Windows API
	dll := syscall.NewLazyDLL("kernel32.dll")
	proc := dll.NewProc("GetUserDefaultLocaleName")
	buf := make([]uint16, 85) // LOCALE_NAME_MAX_LENGTH
	r, _, _ := proc.Call(uintptr(unsafe.Pointer(&buf[0])), 85)
	if r > 0 {
		return syscall.UTF16ToString(buf)
	}
	return ""
}
