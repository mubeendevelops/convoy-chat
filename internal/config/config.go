package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPort           = 8080
	defaultAppEnv         = "development"
	defaultJWTTTL         = 24 * time.Hour
	defaultCORSOrigins    = "http://localhost:3000"
	defaultMigrationsPath = "migrations"
	minJWTSecretLen       = 32
)

// Config holds all runtime configuration loaded from the environment.
type Config struct {
	Port               int
	AppEnv             string
	DatabaseURL        string
	RedisURL           string
	JWTSecret          string
	JWTTTL             time.Duration
	CORSAllowedOrigins []string
	// MigrationsPath is only read by `-migrate` mode (cmd/api/migrate.go); the
	// normal server path never touches it. Defaults to "migrations" relative
	// to the working directory, which the Dockerfile's final stage COPYs to
	// match (see Dockerfile + CLAUDE.md's migration-on-deploy strategy).
	MigrationsPath string
}

// Load reads configuration from environment variables, applying defaults for
// optional values and failing fast if required values are missing or invalid.
func Load() (*Config, error) {
	cfg := &Config{
		Port:               defaultPort,
		AppEnv:             getEnv("APP_ENV", defaultAppEnv),
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		RedisURL:           os.Getenv("REDIS_URL"),
		JWTSecret:          os.Getenv("JWT_SECRET"),
		JWTTTL:             defaultJWTTTL,
		CORSAllowedOrigins: splitAndTrim(getEnv("CORS_ALLOWED_ORIGINS", defaultCORSOrigins)),
		MigrationsPath:     getEnv("MIGRATIONS_PATH", defaultMigrationsPath),
	}

	if v := os.Getenv("PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("parsing PORT: %w", err)
		}
		cfg.Port = port
	}

	if v := os.Getenv("JWT_TTL"); v != "" {
		ttl, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("parsing JWT_TTL: %w", err)
		}
		cfg.JWTTTL = ttl
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Addr returns the address to bind the HTTP server to, e.g. ":8080".
func (c *Config) Addr() string {
	return fmt.Sprintf(":%d", c.Port)
}

func (c *Config) validate() error {
	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if c.RedisURL == "" {
		missing = append(missing, "REDIS_URL")
	}
	if c.JWTSecret == "" {
		missing = append(missing, "JWT_SECRET")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	if len(c.JWTSecret) < minJWTSecretLen {
		return fmt.Errorf("JWT_SECRET must be at least %d characters, got %d", minJWTSecretLen, len(c.JWTSecret))
	}

	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("PORT must be between 1 and 65535, got %d", c.Port)
	}

	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
