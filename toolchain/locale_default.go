//go:build !windows

package main

import (
	"os"
	"strings"
)

func detectLocale() string {
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(key); v != "" {
			v, _, _ = strings.Cut(v, ".")
			v, _, _ = strings.Cut(v, "@")
			return strings.ReplaceAll(v, "_", "-")
		}
	}
	return ""
}
