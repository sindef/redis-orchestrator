package election

import (
	"context"

	"github.com/sindef/redis-orchestrator/pkg/orchestrator/state"
)

// Strategy defines the interface for leader election mechanisms.
// Different strategies (deterministic, Raft) can be plugged in to control
// how the Redis master is selected.
type Strategy interface {
	Start(ctx context.Context) error
	
	Stop() error
	
	IsLeader() bool
	
	GetLeader() (podName string, podUID string, err error)
	
	ElectLeader(ctx context.Context, allStates []*state.PodState, localState *state.PodState) (*state.PodState, error)
	
	Name() string
}

