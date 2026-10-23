package config

import (
	"strings"
	"testing"
)

func TestCheckLocalModeOffPassesAnything(t *testing.T) {
	c := &Config{LocalMode: false, Environment: "production", DBHost: "postgres"}
	if err := c.CheckLocalMode(); err != nil {
		t.Fatalf("LOCAL_MODE off must not refuse: %v", err)
	}
}

func TestCheckLocalModeLoopbackOnly(t *testing.T) {
	cases := []struct {
		host string
		ok   bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"::1", true},
		{"[::1]", true},
		{"localhost", true},
		{"LOCALHOST", true},
		{"defolt-postgres", false},
		{"postgres.infra.svc.cluster.local", false},
		{"10.43.140.158", false},
		{"169.58.46.118", false},
		{"", false},
	}
	for _, tc := range cases {
		c := &Config{LocalMode: true, Environment: "local", DBHost: tc.host}
		err := c.CheckLocalMode()
		if tc.ok && err != nil {
			t.Errorf("host %q: refused, want allowed: %v", tc.host, err)
		}
		if !tc.ok && (err == nil || !strings.Contains(err.Error(), "loopback")) {
			t.Errorf("host %q: err %v, want a loopback refusal", tc.host, err)
		}
	}
}

func TestCheckLocalModeRefusesProduction(t *testing.T) {
	for _, env := range []string{"production", "PRODUCTION", " prod ", "prd"} {
		c := &Config{LocalMode: true, Environment: env, DBHost: "127.0.0.1"}
		if err := c.CheckLocalMode(); err == nil || !strings.Contains(err.Error(), "production") {
			t.Errorf("ENVIRONMENT %q: err %v, want a production refusal", env, err)
		}
	}
}

// Load is where the check runs, so LOCAL_MODE=true with a cluster database host never gets as far as opening a pool.
func TestLoadRefusesLocalModeOnClusterHost(t *testing.T) {
	clearKeys(t)
	t.Setenv("INTERNAL_SERVICE_KEY", "k")
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("ENVIRONMENT", "local")
	t.Setenv("DB_HOST", "defolt-postgres")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "LOCAL_MODE refused") {
		t.Fatalf("Load: err %v, want LOCAL_MODE refused", err)
	}
	t.Setenv("DB_HOST", "127.0.0.1")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load on loopback: %v", err)
	}
	if !c.LocalMode {
		t.Fatal("LOCAL_MODE=true did not set LocalMode")
	}
}
