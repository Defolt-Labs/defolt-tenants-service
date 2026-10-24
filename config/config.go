package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	hk "github.com/Defolt-Labs/defolt-httpkit"
)

type Config struct {
	ServiceName    string
	ServiceVersion string
	Environment    string
	// PublicRootDomain is the shared DNS root for customer-facing hosts.
	PublicRootDomain string
	ServerPort       string

	DBHost, DBPort, DBName, DBUser, DBPassword, DBSSLMode string
	MaxOpenConns, MaxIdleConns                            int
	ConnMaxLifetime                                       time.Duration

	NatsURL string
	// InternalKeys is what the internal routes accept: the pairs of INTERNAL_SERVICE_KEYS when it is set, else the outbound key whole.
	InternalKeys hk.KeySet
	// OutboundServiceKey is the single key sent to identity and billing.
	OutboundServiceKey string
	AutoMigrate        bool

	// LocalMode is a run on a developer's Mac against the UAT stores through the tunnel (LOCAL_MODE=true, office/tools/local-run.sh).
	LocalMode bool

	// RedisURL configures lookup cache for resolve-host.
	RedisURL string

	// ReservedSlugs holds slugs refused for tenant allocation.
	ReservedSlugs []string

	TurnstileSecret string
	IdentityURL     string
	DefaultPlatform string

	// BillingURL points at defolt-billing-service for registration checkout calls.
	BillingURL string
}

func Load() (*Config, error) {
	if data, err := os.ReadFile("/app/.env"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if key, val, ok := strings.Cut(line, "="); ok {
				key = strings.TrimSpace(key)
				val = strings.Trim(strings.TrimSpace(val), "\"")
				if os.Getenv(key) == "" {
					os.Setenv(key, val)
				}
			}
		}
	}

	reserved := strings.Split(getStr("RESERVED_SLUGS", "www,api,app,admin,staff,ci,grafana,docs,tempo,loki,mail,webmail,billing,tenants,drs,mt,uat,shop,staging,secrets,s3,registry,vps"), ",")
	for i, r := range reserved {
		reserved[i] = strings.TrimSpace(r)
	}

	c := &Config{
		ServiceName:      getStr("SERVICE_NAME", "defolt-tenants-service"),
		ServiceVersion:   getStr("SERVICE_VERSION", "v0.1.0"),
		Environment:      getStr("ENVIRONMENT", "development"),
		PublicRootDomain: getStr("PUBLIC_ROOT_DOMAIN", "uat.defoltlabs.com"),
		ServerPort:       getStr("SERVER_PORT", "8080"),

		DBHost:     getStr("DB_HOST", "defolt-postgres"),
		DBPort:     getStr("DB_PORT", "5432"),
		DBName:     getStr("DB_NAME", "defolt_tenants"),
		DBUser:     getStr("DB_USER", "defolt_tenants"),
		DBPassword: getStr("DEFOLT_TENANTS_DB_PASSWORD", ""),
		DBSSLMode:  getStr("DB_SSLMODE", "disable"),

		MaxOpenConns:    getInt("DB_MAX_OPEN_CONNS", 10),
		MaxIdleConns:    getInt("DB_MAX_IDLE_CONNS", 3),
		ConnMaxLifetime: time.Duration(getInt("DB_CONN_MAX_LIFETIME_MIN", 15)) * time.Minute,

		NatsURL:            getStr("NATS_URL", "nats://defolt-nats:4222"),
		OutboundServiceKey: getStr("DEFOLT_INTERNAL_SERVICE_KEY", getStr("INTERNAL_SERVICE_KEY", "")),
		AutoMigrate:        getBool("AUTO_MIGRATE", true),
		LocalMode:          getBool("LOCAL_MODE", false),

		RedisURL:      getStr("REDIS_URL", "redis://defolt-redis:6379"),
		ReservedSlugs: reserved,

		TurnstileSecret: getStr("TURNSTILE_SECRET", ""),
		IdentityURL:     getStr("DEFOLT_IDENTITY_URL", "http://defolt-identity-service:8080"),
		DefaultPlatform: getStr("DEFAULT_PLATFORM", "drs"),

		BillingURL: getStr("DEFOLT_BILLING_URL", "http://defolt-billing-service:8080"),
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	// A malformed INTERNAL_SERVICE_KEYS stops boot; the error names an entry by position, never a value.
	set, err := hk.NewKeySet(c.OutboundServiceKey, os.Getenv("INTERNAL_SERVICE_KEYS"))
	if err != nil {
		return nil, fmt.Errorf("INTERNAL_SERVICE_KEYS: %w", err)
	}
	c.InternalKeys = set
	if err := c.CheckLocalMode(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) Validate() error {
	var missing []string
	for k, v := range map[string]string{
		"DEFOLT_TENANTS_DB_PASSWORD":                          c.DBPassword,
		"DEFOLT_INTERNAL_SERVICE_KEY or INTERNAL_SERVICE_KEY": c.OutboundServiceKey,
	} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("required env vars missing: %s", strings.Join(missing, ", "))
	}
	if strings.Contains(c.OutboundServiceKey, ",") {
		return fmt.Errorf("DEFOLT_INTERNAL_SERVICE_KEY or INTERNAL_SERVICE_KEY holds a comma: it is the one key sent to identity and billing, a named set belongs in INTERNAL_SERVICE_KEYS")
	}
	return nil
}

func (c *Config) DSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s TimeZone=UTC",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode)
}

func getStr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}
func getInt(k string, def int) int {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
func getBool(k string, def bool) bool {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return def
}
