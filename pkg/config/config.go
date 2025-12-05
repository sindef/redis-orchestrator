package config

import "time"

// Config holds the configuration for the Redis orchestrator
type Config struct {
	// Redis connection settings
	RedisHost     string
	RedisPort     int
	RedisPassword string
	RedisTLS      bool
	
	// Service name for replica configuration
	RedisServiceName string
	
	// Sync interval for state checks
	SyncInterval time.Duration
	
	// Kubernetes settings
	PodName       string
	Namespace     string
	LabelSelector string
	
	// Logging
	Debug bool
}

