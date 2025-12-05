package config

import "time"

// ElectionMode specifies the election strategy
type ElectionMode string

const (
	// ElectionModeDeterministic uses timestamp-based deterministic election
	ElectionModeDeterministic ElectionMode = "deterministic"
	// ElectionModeRaft uses Raft consensus for election
	ElectionModeRaft ElectionMode = "raft"
)

// Config holds the configuration for the Redis orchestrator
type Config struct {
	// Redis connection settings
	RedisHost          string
	RedisPort          int
	RedisPassword      string
	RedisTLS           bool
	RedisTLSSkipVerify bool // If true, skip TLS certificate verification
	
	// Service name for replica configuration
	RedisServiceName string
	
	// Sync interval for state checks
	SyncInterval time.Duration
	
	// Kubernetes settings
	PodName       string
	Namespace     string
	LabelSelector string
	
	// Election settings
	ElectionMode    ElectionMode
	RaftBindAddr    string   // Address for Raft to bind to (e.g., "0.0.0.0:7000")
	RaftPeers       []string // List of Raft peer addresses (e.g., ["pod-0:7000", "pod-1:7000"])
	RaftDataDir     string   // Directory for Raft data storage
	RaftBootstrap   bool     // If true, this node will bootstrap the cluster
	
	// Authentication
	SharedSecret string // Shared secret for peer authentication
	
	// Standalone/Witness mode
	Standalone bool // If true, participates in Raft but doesn't manage Redis
	
	// Logging
	Debug bool
}

