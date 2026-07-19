package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ServiceName    string
	ServiceVersion string
	Environment    string
	ServerPort     string

	DBHost, DBPort, DBName, DBUser, DBPassword, DBSSLMode string
	MaxOpenConns, MaxIdleConns                            int
	ConnMaxLifetime                                       time.Duration

	NatsURL            string
	InternalServiceKey string
	AutoMigrate        bool

	// Redis lookup cache for resolve-host (30s TTL). When empty the
	// service falls through to Postgres for every request.
	RedisURL string

	// Reserved slugs — the tenants-service refuses to allocate these.
	// Comma-separated env value; defaults cover the fleet's public
	// subdomains (www, api, admin, staff, ci, grafana, docs).
	ReservedSlugs []string

	// Public signup wiring.
	TurnstileSecret string
	IdentityURL     string
	DefaultPlatform string

	// TurnstileSiteKey is the public half of the Turnstile pair. It is
	// injected into the embedded signup page at serve time (§5.17); the
	// default is Cloudflare's always-pass test key so the page still
	// renders a widget on an unprovisioned environment.
	TurnstileSiteKey string

	// TenantBaseDomain builds the post-signup login address the page
	// shows the owner: "<slug>.<TenantBaseDomain>" (plan §4.1).
	TenantBaseDomain string

	// BillingURL points at defolt-billing-service for the internal
	// registration-checkout call (§5.11).
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

	reserved := strings.Split(getStr("RESERVED_SLUGS", "www,api,admin,staff,ci,grafana,docs,tempo,loki,mail,webmail,billing,drs,mt,uat"), ",")
	for i, r := range reserved {
		reserved[i] = strings.TrimSpace(r)
	}

	c := &Config{
		ServiceName:    getStr("SERVICE_NAME", "defolt-tenants-service"),
		ServiceVersion: getStr("SERVICE_VERSION", "v0.1.0"),
		Environment:    getStr("ENVIRONMENT", "development"),
		ServerPort:     getStr("SERVER_PORT", "8080"),

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
		// Falls back to the cluster-wide INTERNAL_SERVICE_KEY already in
		// platform-env; billing and identity verify against that same value.
		InternalServiceKey: getStr("DEFOLT_INTERNAL_SERVICE_KEY", getStr("INTERNAL_SERVICE_KEY", "")),
		AutoMigrate:        getBool("AUTO_MIGRATE", true),

		RedisURL:      getStr("REDIS_URL", "redis://defolt-redis:6379"),
		ReservedSlugs: reserved,

		TurnstileSecret:  getStr("TURNSTILE_SECRET", ""),
		TurnstileSiteKey: getStr("TURNSTILE_SITE_KEY", "1x00000000000000000000AA"),
		TenantBaseDomain: getStr("TENANT_BASE_DOMAIN", "drs.defoltlabs.com"),
		IdentityURL:      getStr("DEFOLT_IDENTITY_URL", "http://defolt-identity-service:8080"),
		DefaultPlatform:  getStr("DEFAULT_PLATFORM", "drs"),

		BillingURL: getStr("DEFOLT_BILLING_URL", "http://defolt-billing-service:8080"),
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Config) Validate() error {
	var missing []string
	for k, v := range map[string]string{
		"DEFOLT_TENANTS_DB_PASSWORD":  c.DBPassword,
		"DEFOLT_INTERNAL_SERVICE_KEY": c.InternalServiceKey,
	} {
		if strings.TrimSpace(v) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("required env vars missing: %s", strings.Join(missing, ", "))
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
