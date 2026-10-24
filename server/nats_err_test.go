package server

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"
)

func TestNatsConnectErrTextHidesPasswordOnParseFailure(t *testing.T) {
	_, err := nats.Connect("nats://svc:s3cr3tpw@host:notaport")
	if err == nil {
		t.Fatal("a bad port connected")
	}
	got := natsConnectErrText(fmt.Errorf("wrapped: %w", err))
	if strings.Contains(got, "s3cr3tpw") {
		t.Fatalf("password in %q", got)
	}
	if got != "NATS_URL does not parse as a URL" {
		t.Fatalf("got %q", got)
	}
}

func TestNatsConnectErrTextKeepsOtherErrors(t *testing.T) {
	e := errors.New("nats: no servers available for connection")
	if got := natsConnectErrText(e); got != e.Error() {
		t.Fatalf("got %q", got)
	}
}
