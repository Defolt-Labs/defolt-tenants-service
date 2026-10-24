package cache

import (
	"strings"
	"testing"
)

func TestParseRedisURLHidesPasswordOnFailure(t *testing.T) {
	for _, raw := range []string{
		"redis://:s3cr3tpw@host:notaport/0",
		"http://:s3cr3tpw@host:6379/0",
		"redis://:s3cr3tpw@host:6379/notadb",
		"redis://:s3cr3tpw@host:6379/1/2",
		"s3cr3tpw:x@host:6379",
	} {
		_, err := parseRedisURL(raw)
		if err == nil {
			t.Fatalf("%q parsed", raw)
		}
		if strings.Contains(err.Error(), "s3cr3tpw") || err.Error() != "REDIS_URL will not parse" {
			t.Fatalf("%q gave %q", raw, err)
		}
	}
}

func TestParseRedisURLKeepsAGoodURL(t *testing.T) {
	opt, err := parseRedisURL("redis://:pw@redis.internal:6379/2")
	if err != nil {
		t.Fatalf("good URL refused: %v", err)
	}
	if opt.Addr != "redis.internal:6379" || opt.DB != 2 {
		t.Fatalf("addr %q db %d", opt.Addr, opt.DB)
	}
}
