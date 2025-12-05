package election

import (
	"context"

	"github.com/sindef/redis-orchestrator/pkg/orchestrator/state"
)

// Strategy defines the interface for leader election strategies
type Strategy interface {
	// Start initializes the election strategy
	Start(ctx context.Context) error
	
	// Stop gracefully stops the election strategy
	Stop() error
	
	// IsLeader returns true if this instance is the current leader
	IsLeader() bool
	
	// GetLeader returns the current leader's pod name and UID
	GetLeader() (podName string, podUID string, err error)
	
	// ElectLeader performs leader election given the current cluster state
	// Returns the elected leader's state
	ElectLeader(ctx context.Context, allStates []*state.PodState, localState *state.PodState) (*state.PodState, error)
	
	// Name returns the strategy name
	Name() string
}

