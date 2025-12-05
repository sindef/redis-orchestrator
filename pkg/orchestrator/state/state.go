package state

import "time"

// PodState represents the current state of a Redis pod as reported by that pod.
// This is exchanged between orchestrator instances via HTTP for peer discovery.
type PodState struct {
	PodName     string    `json:"pod_name"`
	PodIP       string    `json:"pod_ip"`
	PodUID      string    `json:"pod_uid"`
	Namespace   string    `json:"namespace"`
	IsMaster    bool      `json:"is_master"`
	IsHealthy   bool      `json:"is_healthy"`
	// IsWitness indicates this pod participates in Raft but doesn't manage Redis.
	IsWitness   bool      `json:"is_witness"`
	// StartupTime is used for deterministic election ordering (oldest wins).
	StartupTime time.Time `json:"startup_time"`
	// LastSeen tracks when this state was last updated, used to detect stale entries.
	LastSeen    time.Time `json:"last_seen"`
}

