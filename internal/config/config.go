package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/alvor-technologies/iag-platform-go/corsenv"
)

type Config struct {
	Addr                string
	JWTIssuer           string
	JWKSURL             string
	Audience            string // aud claim the service requires on inbound tokens
	ServiceClientID     string
	ServiceClientSecret string
	AuthTokenURL        string
	GatewayAPIPrefix    string
	CORSOrigin          string
	PublicAPIURL        string
	UsersAPIURL         string // iag-users base (gateway: .../api/v1/users)
	FinanceAPIURL       string // iag-finance base (gateway: .../api/v1/finance)
	AutoMigrate         bool
	KafkaBrokers        []string
	EventBusEnabled     bool
	UploadDir           string
	RemindersInterval   time.Duration

	// Consumer settings (Gap #1: PM subscribes to iag.commercial).
	ConsumerEnabled  bool
	ConsumerGroupID  string
	ConsumerDLQTopic string

	// Archive settings (Gap #4: retention sweep).
	ArchiveInterval time.Duration
}

// Load reads configuration from env. Hard cutover: no AUTH_MODE, no
// GATEWAY_INTERNAL_SECRET — every inbound request must carry a verifiable
// Bearer token with aud=iag.project-management.
func Load() (Config, error) {
	loadDotEnv()
	issuer := envOr("JWT_ISSUER", "http://localhost:3001")
	cfg := Config{
		Addr:                ListenAddr(),
		JWTIssuer:           issuer,
		JWKSURL:             envOr("JWKS_URL", strings.TrimRight(issuer, "/")+"/.well-known/jwks.json"),
		Audience:            envOr("AUDIENCE", "iag.project-management"),
		ServiceClientID:     envOr("SERVICE_CLIENT_ID", "iag-project-management"),
		ServiceClientSecret: strings.TrimSpace(os.Getenv("SERVICE_CLIENT_SECRET")),
		AuthTokenURL:        envOr("AUTH_TOKEN_URL", strings.TrimRight(issuer, "/")+"/oauth/token"),
		GatewayAPIPrefix:    strings.TrimSpace(envOr("GATEWAY_API_PREFIX", "/api/v1/project-management")),
		CORSOrigin:          corsenv.Allowlist(corsenv.DefaultDevOrigins),
		PublicAPIURL:        strings.TrimRight(strings.TrimSpace(envOr("PUBLIC_API_URL", "")), "/"),
		UsersAPIURL:         usersAPIURL(envOr("PUBLIC_API_URL", ""), envOr("USERS_API_URL", "")),
		FinanceAPIURL:       financeAPIURL(envOr("PUBLIC_API_URL", ""), envOr("FINANCE_API_URL", "")),
		AutoMigrate:         envOr("AUTO_MIGRATE", "true") != "false",
		EventBusEnabled:     strings.EqualFold(os.Getenv("EVENT_BUS_ENABLED"), "true"),
		UploadDir:           envOr("PM_UPLOAD_DIR", "./data/pm-uploads"),
		RemindersInterval:   parseDuration("REMINDERS_INTERVAL", 15*time.Minute),
		ConsumerEnabled:     strings.EqualFold(os.Getenv("CONSUMER_ENABLED"), "true"),
		ConsumerGroupID:     envOr("CONSUMER_GROUP_ID", "iag.project-management.commercial"),
		ConsumerDLQTopic:    envOr("CONSUMER_DLQ_TOPIC", "iag.dlq.project-management"),
		ArchiveInterval:     parseDuration("ARCHIVE_INTERVAL", 24*time.Hour),
	}
	if brokers := strings.TrimSpace(os.Getenv("KAFKA_BROKERS")); brokers != "" {
		for _, b := range strings.Split(brokers, ",") {
			if t := strings.TrimSpace(b); t != "" {
				cfg.KafkaBrokers = append(cfg.KafkaBrokers, t)
			}
		}
	}
	if cfg.EventBusEnabled && len(cfg.KafkaBrokers) == 0 {
		cfg.KafkaBrokers = []string{"127.0.0.1:19092"}
	}

	if cfg.Audience == "" {
		return Config{}, fmt.Errorf("AUDIENCE is required (e.g. iag.project-management)")
	}
	if cfg.JWKSURL == "" {
		return Config{}, fmt.Errorf("JWKS_URL is required")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func usersAPIURL(publicAPI, explicit string) string {
	return serviceAPIURL(publicAPI, explicit, "http://localhost:8080/api/v1/users", "/api/v1/users")
}

func financeAPIURL(publicAPI, explicit string) string {
	return serviceAPIURL(publicAPI, explicit, "http://localhost:8080/api/v1/finance", "/api/v1/finance")
}

func serviceAPIURL(publicAPI, explicit, localDefault, suffix string) string {
	if u := strings.TrimRight(strings.TrimSpace(explicit), "/"); u != "" {
		return u
	}
	publicAPI = strings.TrimRight(strings.TrimSpace(publicAPI), "/")
	if publicAPI == "" {
		return localDefault
	}
	return publicAPI + suffix
}

// parseDuration reads a duration env var (e.g. "15m", "24h"). Non-positive or
// unparsable values fall back to fallback and log a warning.
func parseDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		slog.Warn("invalid duration env var; using fallback", "key", key, "value", raw, "fallback", fallback)
		return fallback
	}
	return d
}
