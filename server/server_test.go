package server

import "testing"

func TestNatsStateIsAbsentWithNoConnection(t *testing.T) {
	if got := natsState(nil)(); got != "absent" {
		t.Fatalf("natsState(nil) = %q, want absent", got)
	}
}
