package config

import (
	"reflect"
	"strings"
	"testing"

	hk "github.com/Defolt-Labs/defolt-httpkit"
)

func clearKeys(t *testing.T) {
	t.Helper()
	for _, k := range []string{"INTERNAL_SERVICE_KEYS", "DEFOLT_INTERNAL_SERVICE_KEY", "INTERNAL_SERVICE_KEY"} {
		t.Setenv(k, "")
	}
	t.Setenv("DEFOLT_TENANTS_DB_PASSWORD", "pw")
}

func TestLoadSplitsInboundSetFromOutboundKey(t *testing.T) {
	clearKeys(t)
	t.Setenv("INTERNAL_SERVICE_KEYS", "platform=k1,dhs=k2,drs=k3")
	t.Setenv("INTERNAL_SERVICE_KEY", "k1")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for presented, want := range map[string]string{"k1": "platform", "k2": "dhs", "k3": "drs"} {
		if got, ok := c.InternalKeys.Match(presented); !ok || got != want {
			t.Fatalf("inbound Match(%q) = %q, %v; want %q", presented, got, ok, want)
		}
	}
	if c.OutboundServiceKey != "k1" {
		t.Fatalf("outbound = %q, want the single key", c.OutboundServiceKey)
	}
}

func TestLoadOutboundPrefersDefoltKey(t *testing.T) {
	clearKeys(t)
	t.Setenv("DEFOLT_INTERNAL_SERVICE_KEY", "own")
	t.Setenv("INTERNAL_SERVICE_KEY", "fleet")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := c.InternalKeys.Match("own"); !ok || c.OutboundServiceKey != "own" {
		t.Fatalf("outbound %q, want own for both directions", c.OutboundServiceKey)
	}
	if _, ok := c.InternalKeys.Match("fleet"); ok {
		t.Fatal("the fleet key was accepted beside the service's own")
	}
}

func TestLoadBareKeyServesBothDirections(t *testing.T) {
	clearKeys(t)
	t.Setenv("INTERNAL_SERVICE_KEY", "only")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := c.InternalKeys.Match("only"); !ok || c.OutboundServiceKey != "only" {
		t.Fatalf("outbound %q, want only for both directions", c.OutboundServiceKey)
	}
}

func TestLoadRefusesWithoutAnyKey(t *testing.T) {
	clearKeys(t)
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a config with no internal key")
	}
}

func TestLoadRefusesWhenOnlyTheSetIsGiven(t *testing.T) {
	clearKeys(t)
	t.Setenv("INTERNAL_SERVICE_KEYS", "platform=k1")
	if _, err := Load(); err == nil {
		t.Fatal("Load accepted a set with no outbound key")
	}
}

func TestLoadRefusesACommaInTheOutboundKey(t *testing.T) {
	for _, name := range []string{"DEFOLT_INTERNAL_SERVICE_KEY", "INTERNAL_SERVICE_KEY"} {
		clearKeys(t)
		t.Setenv(name, "platform=s3cr3t,dhs=other")
		_, err := Load()
		if err == nil {
			t.Fatalf("%s holding a comma was accepted", name)
		}
		if strings.Contains(err.Error(), "s3cr3t") {
			t.Fatalf("%s: the refusal repeats the key: %v", name, err)
		}
	}
}

func TestLoadNamedSetWinsOverTheSingleKey(t *testing.T) {
	clearKeys(t)
	t.Setenv("INTERNAL_SERVICE_KEYS", "platform=k1")
	t.Setenv("INTERNAL_SERVICE_KEY", "old")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := c.InternalKeys.Match("old"); ok {
		t.Fatal("the single key was accepted beside a named set")
	}
	if c.InternalKeys.Source() != hk.KeySourceNamed {
		t.Fatalf("source = %q, want %q", c.InternalKeys.Source(), hk.KeySourceNamed)
	}
}

func TestLoadRefusesAMalformedNamedSetWithoutRepeatingIt(t *testing.T) {
	for _, bad := range []string{"platform=s3cr3t,s3cr3tbare", "Platform=s3cr3t", "platform=", "a=s3cr3t,a=other"} {
		clearKeys(t)
		t.Setenv("INTERNAL_SERVICE_KEYS", bad)
		t.Setenv("INTERNAL_SERVICE_KEY", "k1")
		_, err := Load()
		if err == nil {
			t.Fatalf("%q was accepted", bad)
		}
		if strings.Contains(err.Error(), "s3cr3t") || strings.Contains(err.Error(), "other") {
			t.Fatalf("the refusal repeats a value: %v", err)
		}
	}
}

// Load runs the local-mode check, so LOCAL_MODE=true with a cluster database host never reaches a pool.
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

func TestLoadRefusesLocalModeOnProduction(t *testing.T) {
	clearKeys(t)
	t.Setenv("INTERNAL_SERVICE_KEY", "k")
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("DB_HOST", "127.0.0.1")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "production") {
		t.Fatalf("Load: err %v, want a production refusal", err)
	}
}

// An unset ENVIRONMENT defaults to development for other uses but must not count as declared, so the human check cannot be skipped by omission.
func TestDeclaredEnvironment(t *testing.T) {
	clearKeys(t)
	t.Setenv("INTERNAL_SERVICE_KEY", "k")
	t.Setenv("ENVIRONMENT", "")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Environment != "development" || c.DeclaredEnvironment() != "" {
		t.Fatalf("unset: Environment %q, declared %q, want development and blank", c.Environment, c.DeclaredEnvironment())
	}
	t.Setenv("ENVIRONMENT", "development")
	c, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DeclaredEnvironment() != "development" {
		t.Fatalf("set: declared %q, want development", c.DeclaredEnvironment())
	}
}

func TestLoadOwnerLinkNamespaces(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"health", []string{"health"}},
		{" Health , afya,, ", []string{"health", "afya"}},
	} {
		clearKeys(t)
		t.Setenv("INTERNAL_SERVICE_KEY", "k")
		t.Setenv("OWNER_LINK_NAMESPACES", tc.raw)
		c, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !reflect.DeepEqual(c.OwnerLinkNamespaces, tc.want) {
			t.Errorf("OWNER_LINK_NAMESPACES %q = %v, want %v", tc.raw, c.OwnerLinkNamespaces, tc.want)
		}
	}
}
