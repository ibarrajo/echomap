package config_test

import (
	"testing"
	"time"

	"github.com/elninja/echomap/internal/config"
)

func TestDefaults(t *testing.T) {
	cfg := config.New()

	if cfg.GRPCPort != 50051 {
		t.Errorf("default gRPC port should be 50051, got %d", cfg.GRPCPort)
	}
	if cfg.TokenTTL != 10*time.Second {
		t.Errorf("default token TTL should be 10s, got %v", cfg.TokenTTL)
	}
	if cfg.ProbeCount != 6 {
		t.Errorf("default probe count should be 6, got %d", cfg.ProbeCount)
	}
	if cfg.PingCount != 3 {
		t.Errorf("default ping count should be 3, got %d", cfg.PingCount)
	}
	if cfg.TimeoutMS != 5000 {
		t.Errorf("default timeout should be 5000ms, got %d", cfg.TimeoutMS)
	}
	if cfg.HMACSecret == "" {
		t.Error("HMAC secret should have a default for development")
	}
}

func TestFromEnv_OverridesDefaults(t *testing.T) {
	t.Setenv("ECHOMAP_GRPC_PORT", "9090")
	t.Setenv("ECHOMAP_TOKEN_TTL", "30s")
	t.Setenv("ECHOMAP_PROBE_COUNT", "8")
	t.Setenv("ECHOMAP_PING_COUNT", "5")
	t.Setenv("ECHOMAP_TIMEOUT_MS", "10000")
	t.Setenv("ECHOMAP_HMAC_SECRET", "my-production-secret-key-32b!!!!")

	cfg := config.New()

	if cfg.GRPCPort != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.GRPCPort)
	}
	if cfg.TokenTTL != 30*time.Second {
		t.Errorf("expected TTL 30s, got %v", cfg.TokenTTL)
	}
	if cfg.ProbeCount != 8 {
		t.Errorf("expected probe count 8, got %d", cfg.ProbeCount)
	}
	if cfg.PingCount != 5 {
		t.Errorf("expected ping count 5, got %d", cfg.PingCount)
	}
	if cfg.TimeoutMS != 10000 {
		t.Errorf("expected timeout 10000, got %d", cfg.TimeoutMS)
	}
	if cfg.HMACSecret != "my-production-secret-key-32b!!!!" {
		t.Errorf("expected custom HMAC secret, got %s", cfg.HMACSecret)
	}
}

func TestFromEnv_InvalidPort_UsesDefault(t *testing.T) {
	t.Setenv("ECHOMAP_GRPC_PORT", "not-a-number")
	cfg := config.New()
	if cfg.GRPCPort != 50051 {
		t.Errorf("invalid port should fall back to default, got %d", cfg.GRPCPort)
	}
}

func TestFromEnv_InvalidTTL_UsesDefault(t *testing.T) {
	t.Setenv("ECHOMAP_TOKEN_TTL", "garbage")
	cfg := config.New()
	if cfg.TokenTTL != 10*time.Second {
		t.Errorf("invalid TTL should fall back to default, got %v", cfg.TokenTTL)
	}
}
