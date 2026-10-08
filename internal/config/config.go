package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr      string
	DSN           string
	JWTSecret     string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	MigrationsDir   string
	CorsAllowedOrigins []string
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load reads configuration from environment variables with sane defaults.
func Load() (*Config, error) {
	secret := getenv("JWT_SECRET", "")
	if secret == "" {
		return nil, fmt.Errorf("JWT_SECRET must be set")
	}
	accTTL, err := time.ParseDuration(getenv("ACCESS_TOKEN_TTL", "30m"))
	if err != nil {
		return nil, fmt.Errorf("invalid ACCESS_TOKEN_TTL: %w", err)
	}
	refTTL, err := time.ParseDuration(getenv("REFRESH_TOKEN_TTL", "720h"))
	if err != nil {
		return nil, fmt.Errorf("invalid REFRESH_TOKEN_TTL: %w", err)
	}
	port, err := strconv.Atoi(getenv("PORT", "8080"))
	if err != nil {
		return nil, fmt.Errorf("invalid PORT: %w", err)
	}
	c := &Config{
		HTTPAddr:          ":" + strconv.Itoa(port),
		DSN:               getenv("DATABASE_URL", "postgres://chatter:chatter@localhost:5432/chatter?sslmode=disable"),
		JWTSecret:         secret,
		AccessTokenTTL:    accTTL,
		RefreshTokenTTL:   refTTL,
		MigrationsDir:     getenv("MIGRATIONS_DIR", "migrations"),
		CorsAllowedOrigins: []string{"*"},
	}
	return c, nil }
