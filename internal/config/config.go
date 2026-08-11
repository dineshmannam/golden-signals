// Package config provides tiny typed helpers for reading configuration from the
// environment with defaults. The services are configured entirely through env
// vars (12-factor style) so the same image runs unchanged across environments.
package config

import (
	"os"
	"strconv"
	"time"
)

// String returns the value of key, or def if unset/empty.
func String(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Int returns the integer value of key, or def if unset or unparseable.
func Int(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// Duration returns the duration value of key (e.g. "5s"), or def.
func Duration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
