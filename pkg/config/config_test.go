package config

import (
	"testing"
	"time"
)

func TestConfigDefaults(t *testing.T) {
	cfg := &Config{}

	if cfg.RedisHost != "" {
		t.Errorf("Expected empty RedisHost by default, got %s", cfg.RedisHost)
	}

	if cfg.RedisPort != 0 {
		t.Errorf("Expected 0 RedisPort by default, got %d", cfg.RedisPort)
	}

	if cfg.RedisTLS != false {
		t.Error("Expected RedisTLS to be false by default")
	}

	if cfg.SyncInterval != 0 {
		t.Errorf("Expected 0 SyncInterval by default, got %v", cfg.SyncInterval)
	}
}

func TestConfigWithValues(t *testing.T) {
	cfg := &Config{
		RedisHost:        "localhost",
		RedisPort:        6379,
		RedisPassword:    "secret",
		RedisTLS:         true,
		RedisServiceName: "redis-service",
		SyncInterval:     15 * time.Second,
		PodName:          "redis-0",
		Namespace:        "default",
		LabelSelector:    "app=redis",
	}

	if cfg.RedisHost != "localhost" {
		t.Errorf("Expected RedisHost localhost, got %s", cfg.RedisHost)
	}

	if cfg.RedisPort != 6379 {
		t.Errorf("Expected RedisPort 6379, got %d", cfg.RedisPort)
	}

	if cfg.RedisPassword != "secret" {
		t.Errorf("Expected RedisPassword secret, got %s", cfg.RedisPassword)
	}

	if !cfg.RedisTLS {
		t.Error("Expected RedisTLS to be true")
	}

	if cfg.RedisServiceName != "redis-service" {
		t.Errorf("Expected RedisServiceName redis-service, got %s", cfg.RedisServiceName)
	}

	if cfg.SyncInterval != 15*time.Second {
		t.Errorf("Expected SyncInterval 15s, got %v", cfg.SyncInterval)
	}

	if cfg.PodName != "redis-0" {
		t.Errorf("Expected PodName redis-0, got %s", cfg.PodName)
	}

	if cfg.Namespace != "default" {
		t.Errorf("Expected Namespace default, got %s", cfg.Namespace)
	}

	if cfg.LabelSelector != "app=redis" {
		t.Errorf("Expected LabelSelector app=redis, got %s", cfg.LabelSelector)
	}
}

func TestConfigPasswordHandling(t *testing.T) {
	tests := []struct {
		name     string
		password string
		isEmpty  bool
	}{
		{
			name:     "with password",
			password: "mypassword",
			isEmpty:  false,
		},
		{
			name:     "empty password",
			password: "",
			isEmpty:  true,
		},
		{
			name:     "whitespace password",
			password: "   ",
			isEmpty:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				RedisPassword: tt.password,
			}

			isEmpty := cfg.RedisPassword == ""
			if isEmpty != tt.isEmpty {
				t.Errorf("Expected isEmpty=%v, got %v", tt.isEmpty, isEmpty)
			}
		})
	}
}

func TestConfigSyncIntervalValues(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		valid    bool
	}{
		{
			name:     "15 seconds",
			interval: 15 * time.Second,
			valid:    true,
		},
		{
			name:     "5 seconds",
			interval: 5 * time.Second,
			valid:    true,
		},
		{
			name:     "1 minute",
			interval: 1 * time.Minute,
			valid:    true,
		},
		{
			name:     "zero interval",
			interval: 0,
			valid:    false,
		},
		{
			name:     "negative interval",
			interval: -1 * time.Second,
			valid:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				SyncInterval: tt.interval,
			}

			valid := cfg.SyncInterval > 0
			if valid != tt.valid {
				t.Errorf("Expected valid=%v for interval %v, got %v", tt.valid, cfg.SyncInterval, valid)
			}
		})
	}
}

