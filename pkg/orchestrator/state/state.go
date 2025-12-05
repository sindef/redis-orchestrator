package state

import "time"

// PodState represents the state of a Redis pod
type PodState struct {
	PodName     string    `json:"pod_name"`
	PodIP       string    `json:"pod_ip"`
	PodUID      string    `json:"pod_uid"`
	Namespace   string    `json:"namespace"`
	IsMaster    bool      `json:"is_master"`
	IsHealthy   bool      `json:"is_healthy"`
	IsWitness   bool      `json:"is_witness"`   // True if running in standalone/witness mode
	StartupTime time.Time `json:"startup_time"`
	LastSeen    time.Time `json:"last_seen"`
}

