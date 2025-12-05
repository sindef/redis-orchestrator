package election

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/sindef/redis-orchestrator/pkg/orchestrator/state"
	"k8s.io/klog/v2"
)

// DeterministicStrategy implements leader election using deterministic sorting
type DeterministicStrategy struct {
	localPodName string
	localPodUID  string
	debug        bool
	
	mu           sync.RWMutex
	currentLeader *state.PodState
}

// NewDeterministicStrategy creates a new deterministic election strategy
func NewDeterministicStrategy(podName, podUID string, debug bool) *DeterministicStrategy {
	return &DeterministicStrategy{
		localPodName: podName,
		localPodUID:  podUID,
		debug:        debug,
	}
}

// Start initializes the strategy
func (d *DeterministicStrategy) Start(ctx context.Context) error {
	if d.debug {
		klog.Info("Started deterministic election strategy")
	}
	return nil
}

// Stop gracefully stops the strategy
func (d *DeterministicStrategy) Stop() error {
	if d.debug {
		klog.Info("Stopped deterministic election strategy")
	}
	return nil
}

// IsLeader returns true if this instance is the current leader
func (d *DeterministicStrategy) IsLeader() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	
	if d.currentLeader == nil {
		return false
	}
	return d.currentLeader.PodUID == d.localPodUID
}

// GetLeader returns the current leader
func (d *DeterministicStrategy) GetLeader() (string, string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	
	if d.currentLeader == nil {
		return "", "", fmt.Errorf("no leader elected")
	}
	return d.currentLeader.PodName, d.currentLeader.PodUID, nil
}

// ElectLeader performs deterministic leader election
func (d *DeterministicStrategy) ElectLeader(ctx context.Context, allStates []*state.PodState, localState *state.PodState) (*state.PodState, error) {
	if len(allStates) == 0 {
		return nil, fmt.Errorf("no healthy pods available for election")
	}

	if d.debug {
		klog.Info("========================================")
		klog.Info("DETERMINISTIC ELECTION - Starting")
		klog.InfoS("Election candidates", "count", len(allStates))
		for i, st := range allStates {
			klog.InfoS("Candidate",
				"rank", i+1,
				"pod", st.PodName,
				"uid", st.PodUID,
				"namespace", st.Namespace,
				"startupTime", st.StartupTime.Format("2006-01-02T15:04:05Z07:00"),
				"ip", st.PodIP)
		}
	}

	// Sort by:
	// 1. Startup time (oldest first)
	// 2. Pod name (lexicographically)
	// 3. UID (lexicographically) - for multi-site
	sort.Slice(allStates, func(i, j int) bool {
		if allStates[i].StartupTime.Equal(allStates[j].StartupTime) {
			if allStates[i].PodName == allStates[j].PodName {
				return allStates[i].PodUID < allStates[j].PodUID
			}
			return allStates[i].PodName < allStates[j].PodName
		}
		return allStates[i].StartupTime.Before(allStates[j].StartupTime)
	})

	elected := allStates[0]
	
	if d.debug {
		klog.Info("Election result:")
		klog.InfoS("ELECTED LEADER",
			"pod", elected.PodName,
			"uid", elected.PodUID,
			"namespace", elected.Namespace,
			"startupTime", elected.StartupTime.Format("2006-01-02T15:04:05Z07:00"),
			"reason", d.getElectionReason(allStates))
	}

	// Update internal state
	d.mu.Lock()
	d.currentLeader = elected
	d.mu.Unlock()

	return elected, nil
}

// Name returns the strategy name
func (d *DeterministicStrategy) Name() string {
	return "deterministic"
}

// getElectionReason explains why the elected leader was chosen
func (d *DeterministicStrategy) getElectionReason(sortedStates []*state.PodState) string {
	if len(sortedStates) < 2 {
		return "only candidate"
	}
	
	elected := sortedStates[0]
	runner := sortedStates[1]
	
	if !elected.StartupTime.Equal(runner.StartupTime) {
		return fmt.Sprintf("oldest startup time (%s vs %s)",
			elected.StartupTime.Format("2006-01-02T15:04:05Z07:00"),
			runner.StartupTime.Format("2006-01-02T15:04:05Z07:00"))
	}
	
	if elected.PodName != runner.PodName {
		return fmt.Sprintf("tie-breaker: pod name (%s < %s)", elected.PodName, runner.PodName)
	}
	
	return fmt.Sprintf("tie-breaker: pod UID (%s < %s) - multi-site scenario", 
		elected.PodUID[:8], runner.PodUID[:8])
}

