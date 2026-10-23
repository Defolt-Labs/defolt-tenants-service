package middleware

import "testing"

func TestKeySetMatchesEachNamedEntry(t *testing.T) {
	s := parseKeySet("platform=k1, dhs=k2,drs=k3")
	for presented, want := range map[string]string{"k1": "platform", "k2": "dhs", "k3": "drs"} {
		got, ok := s.match(presented)
		if !ok || got != want {
			t.Fatalf("match(%q) = %q, %v; want %q", presented, got, ok, want)
		}
	}
}

func TestKeySetRefusesOldAndEmptyAndTheWholeSet(t *testing.T) {
	raw := "platform=k1,dhs=k2,drs=k3"
	s := parseKeySet(raw)
	for _, presented := range []string{"", "old", raw} {
		if _, ok := s.match(presented); ok {
			t.Fatalf("match(%q) accepted", presented)
		}
	}
}

func TestKeySetBareKeyIsLegacy(t *testing.T) {
	got, ok := parseKeySet("bare").match("bare")
	if !ok || got != "legacy" {
		t.Fatalf("got %q, %v", got, ok)
	}
}

func TestKeySetEmptyRefusesEverything(t *testing.T) {
	if _, ok := parseKeySet("").match("x"); ok {
		t.Fatal("empty set accepted a key")
	}
}
