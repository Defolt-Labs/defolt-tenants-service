package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	hk "github.com/Defolt-Labs/defolt-httpkit"
	"github.com/Defolt-Labs/defolt-svckit/envx"
	"github.com/Defolt-Labs/defolt-svckit/localmode"
)

type Config struct {
	ServiceName    string
	ServiceVersion string
	Environment    string
	// EnvironmentSet is true only when ENVIRONMENT was given; the "development" default must never switch a safety check off.
	EnvironmentSet bool
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

	reserved := strings.Split(envx.String("RESERVED_SLUGS", "www,api,app,admin,staff,ci,grafana,docs,tempo,loki,mail,webmail,billing,tenants,drs,mt,uat,shop,staging,secrets,s3,registry,vps"), ",")
	for i, r := range reserved {
		reserved[i] = strings.TrimSpace(r)
	}

	c := &Config{
		ServiceName:      envx.String("SERVICE_NAME", "defolt-tenants-service"),
		ServiceVersion:   envx.String("SERVICE_VERSION", "v0.1.0"),
		Environment:      envx.String("ENVIRONMENT", "development"),
		EnvironmentSet:   strings.TrimSpace(os.Getenv("ENVIRONMENT")) != "",
		PublicRootDomain: envx.String("PUBLIC_ROOT_DOMAIN", "uat.defoltlabs.com"),
		ServerPort:       envx.String("SERVER_PORT", "8080"),

		DBHost:     envx.String("DB_HOST", "defolt-postgres"),
		DBPort:     envx.String("DB_PORT", "5432"),
		DBName:     envx.String("DB_NAME", "defolt_tenants"),
		DBUser:     envx.String("DB_USER", "defolt_tenants"),
		DBPassword: envx.String("DEFOLT_TENANTS_DB_PASSWORD", ""),
		DBSSLMode:  envx.String("DB_SSLMODE", "disable"),

		MaxOpenConns:    envx.Int("DB_MAX_OPEN_CONNS", 10),
		MaxIdleConns:    envx.Int("DB_MAX_IDLE_CONNS", 3),
		ConnMaxLifetime: time.Duration(envx.Int("DB_CONN_MAX_LIFETIME_MIN", 15)) * time.Minute,

		NatsURL:            envx.String("NATS_URL", "nats://defolt-nats:4222"),
		OutboundServiceKey: envx.String("DEFOLT_INTERNAL_SERVICE_KEY", envx.String("INTERNAL_SERVICE_KEY", "")),
		AutoMigrate:        envx.Bool("AUTO_MIGRATE", true),
		LocalMode:          envx.Bool("LOCAL_MODE", false),

		RedisURL:      envx.String("REDIS_URL", "redis://defolt-redis:6379"),
		ReservedSlugs: reserved,

		TurnstileSecret: envx.String("TURNSTILE_SECRET", ""),
		IdentityURL:     envx.String("DEFOLT_IDENTITY_URL", "http://defolt-identity-service:8080"),
		DefaultPlatform: envx.String("DEFAULT_PLATFORM", "drs"),

		BillingURL: envx.String("DEFOLT_BILLING_URL", "http://defolt-billing-service:8080"),
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
	if err := localmode.Check(c.LocalMode, c.Environment, c.DBHost, ""); err != nil {
		return nil, err
	}
	return c, nil
}

// DeclaredEnvironment is ENVIRONMENT as set, or "" when it was left unset.
func (c *Config) DeclaredEnvironment() string {
	if !c.EnvironmentSet {
		return ""
	}
	return c.Environment
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
