package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all EchoMap configuration.
type Config struct {
	GRPCPort        int
	TokenTTL        time.Duration
	ProbeCount      int
	PingCount       int
	TimeoutMS       int
	HMACSecret      string
	DBPath          string
	RateLimitMax    int
	RateLimitWindow time.Duration
	DatasetPath     string
}

// New creates a Config, reading from environment variables with sensible defaults.
func New() Config {
	return Config{
		GRPCPort:        envInt("ECHOMAP_GRPC_PORT", 50051),
		TokenTTL:        envDuration("ECHOMAP_TOKEN_TTL", 10*time.Second),
		ProbeCount:      envInt("ECHOMAP_PROBE_COUNT", 6),
		PingCount:       envInt("ECHOMAP_PING_COUNT", 3),
		TimeoutMS:       envInt("ECHOMAP_TIMEOUT_MS", 5000),
		HMACSecret:      envString("ECHOMAP_HMAC_SECRET", "echomap-dev-secret-key-32bytes!!"),
		DBPath:          envString("ECHOMAP_DB_PATH", "echomap.db"),
		RateLimitMax:    envInt("ECHOMAP_RATE_LIMIT_MAX", 10),
		RateLimitWindow: envDuration("ECHOMAP_RATE_LIMIT_WINDOW", time.Minute),
		DatasetPath:     envString("ECHOMAP_DATASET_PATH", ""),
	}
}

func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
