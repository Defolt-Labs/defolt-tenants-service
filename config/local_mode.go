package config

import (
	"fmt"
	"net"
	"strings"
)

// CheckLocalMode reports why this configuration may not run in local mode, or nil.
func (c *Config) CheckLocalMode() error {
	if !c.LocalMode {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(c.Environment)) {
	case "production", "prod", "prd":
		return fmt.Errorf("LOCAL_MODE refused: ENVIRONMENT is %q; a local run never serves production", c.Environment)
	}
	if !IsLoopback(c.DBHost) {
		return fmt.Errorf("LOCAL_MODE refused: database host %q is not a loopback address; a local run reaches the database only through the tunnel on 127.0.0.1", c.DBHost)
	}
	return nil
}

// IsLoopback reports whether host is 127.0.0.0/8, ::1 or localhost.
func IsLoopback(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
