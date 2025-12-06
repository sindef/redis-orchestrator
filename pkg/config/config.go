package config

import "time"

// ElectionMode determines how the orchestrator selects the Redis master.
// Deterministic: uses startup time and pod name/UID for consistent ordering.
// Raft: uses HashiCorp Raft consensus for distributed leader election.
type ElectionMode string

const (
	ElectionModeDeterministic ElectionMode = "deterministic"
	ElectionModeRaft ElectionMode = "raft"
)

type Config struct {
	RedisHost          string
	RedisPort          int
	RedisPassword      string
	RedisTLS           bool
	RedisTLSSkipVerify bool
	
	RedisServiceName string
	
	SyncInterval time.Duration
	
	PodName       string
	Namespace     string
	LabelSelector string
	
	ElectionMode    ElectionMode
	RaftBindAddr    string
	RaftPeers       []string
	RaftDataDir     string
	RaftBootstrap   bool
	DiscoverLBService bool // If true, discover LoadBalancer service with pod-name label selector
	
	SharedSecret string
	
	// Standalone enables witness mode: participates in Raft elections but
	// doesn't manage Redis. Useful for maintaining quorum without extra Redis instances.
	Standalone bool
	
	Debug bool
}

