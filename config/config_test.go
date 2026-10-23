package config

import "testing"

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
	if c.InternalServiceKey != "platform=k1,dhs=k2,drs=k3" {
		t.Fatalf("inbound = %q, want the named set", c.InternalServiceKey)
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
	if c.OutboundServiceKey != "own" || c.InternalServiceKey != "own" {
		t.Fatalf("inbound %q outbound %q, want own for both", c.InternalServiceKey, c.OutboundServiceKey)
	}
}

func TestLoadBareKeyServesBothDirections(t *testing.T) {
	clearKeys(t)
	t.Setenv("INTERNAL_SERVICE_KEY", "only")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.InternalServiceKey != "only" || c.OutboundServiceKey != "only" {
		t.Fatalf("inbound %q outbound %q, want only for both", c.InternalServiceKey, c.OutboundServiceKey)
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
