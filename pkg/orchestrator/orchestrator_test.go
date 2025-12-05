package orchestrator

import (
	"sort"
	"testing"
	"time"

	"github.com/sindef/redis-orchestrator/pkg/orchestrator/state"
)

func TestLeaderElectionByStartupTime(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		states   []*state.PodState
		expected string
	}{
		{
			name: "oldest pod elected",
			states: []*state.PodState{
				{
					PodName:     "redis-0",
					PodUID:      "uid-0",
					StartupTime: now.Add(-10 * time.Minute),
					IsHealthy:   true,
				},
				{
					PodName:     "redis-1",
					PodUID:      "uid-1",
					StartupTime: now.Add(-5 * time.Minute),
					IsHealthy:   true,
				},
				{
					PodName:     "redis-2",
					PodUID:      "uid-2",
					StartupTime: now.Add(-1 * time.Minute),
					IsHealthy:   true,
				},
			},
			expected: "redis-0",
		},
		{
			name: "newest pod with oldest in middle",
			states: []*state.PodState{
				{
					PodName:     "redis-0",
					PodUID:      "uid-0",
					StartupTime: now.Add(-5 * time.Minute),
					IsHealthy:   true,
				},
				{
					PodName:     "redis-1",
					PodUID:      "uid-1",
					StartupTime: now.Add(-10 * time.Minute),
					IsHealthy:   true,
				},
				{
					PodName:     "redis-2",
					PodUID:      "uid-2",
					StartupTime: now.Add(-1 * time.Minute),
					IsHealthy:   true,
				},
			},
			expected: "redis-1",
		},
		{
			name: "single pod",
			states: []*state.PodState{
				{
					PodName:     "redis-0",
					PodUID:      "uid-0",
					StartupTime: now,
					IsHealthy:   true,
				},
			},
			expected: "redis-0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Sort by startup time (oldest first), then by pod name, then by UID
			sort.Slice(tt.states, func(i, j int) bool {
				if tt.states[i].StartupTime.Equal(tt.states[j].StartupTime) {
					if tt.states[i].PodName == tt.states[j].PodName {
						return tt.states[i].PodUID < tt.states[j].PodUID
					}
					return tt.states[i].PodName < tt.states[j].PodName
				}
				return tt.states[i].StartupTime.Before(tt.states[j].StartupTime)
			})

			elected := tt.states[0]
			if elected.PodName != tt.expected {
				t.Errorf("Expected %s to be elected, got %s", tt.expected, elected.PodName)
			}
		})
	}
}

func TestStartupTimeTieBreaker(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		states   []*state.PodState
		expected string
	}{
		{
			name: "identical startup times, lexicographic order",
			states: []*state.PodState{
				{
					PodName:     "redis-2",
					PodUID:      "uid-2",
					StartupTime: now,
					IsHealthy:   true,
				},
				{
					PodName:     "redis-0",
					PodUID:      "uid-0",
					StartupTime: now,
					IsHealthy:   true,
				},
				{
					PodName:     "redis-1",
					PodUID:      "uid-1",
					StartupTime: now,
					IsHealthy:   true,
				},
			},
			expected: "redis-0",
		},
		{
			name: "two pods same time",
			states: []*state.PodState{
				{
					PodName:     "redis-b",
					PodUID:      "uid-b",
					StartupTime: now,
					IsHealthy:   true,
				},
				{
					PodName:     "redis-a",
					PodUID:      "uid-a",
					StartupTime: now,
					IsHealthy:   true,
				},
			},
			expected: "redis-a",
		},
		{
			name: "mixed times with ties",
			states: []*state.PodState{
				{
					PodName:     "redis-2",
					PodUID:      "uid-2",
					StartupTime: now.Add(-5 * time.Minute),
					IsHealthy:   true,
				},
				{
					PodName:     "redis-0",
					PodUID:      "uid-0",
					StartupTime: now.Add(-5 * time.Minute),
					IsHealthy:   true,
				},
				{
					PodName:     "redis-1",
					PodUID:      "uid-1",
					StartupTime: now.Add(-3 * time.Minute),
					IsHealthy:   true,
				},
			},
			expected: "redis-0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Sort by startup time (oldest first), then by pod name, then by UID
			sort.Slice(tt.states, func(i, j int) bool {
				if tt.states[i].StartupTime.Equal(tt.states[j].StartupTime) {
					if tt.states[i].PodName == tt.states[j].PodName {
						return tt.states[i].PodUID < tt.states[j].PodUID
					}
					return tt.states[i].PodName < tt.states[j].PodName
				}
				return tt.states[i].StartupTime.Before(tt.states[j].StartupTime)
			})

			elected := tt.states[0]
			if elected.PodName != tt.expected {
				t.Errorf("Expected %s to be elected, got %s", tt.expected, elected.PodName)
			}
		})
	}
}

func TestMultiSiteScenario(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name        string
		states      []*state.PodState
		expectedPod string
		expectedUID string
	}{
		{
			name: "same pod name different sites - UID tie-breaker",
			states: []*state.PodState{
				{
					PodName:     "redis-0",
					PodUID:      "uid-site2-abc",
					Namespace:   "redis-site2",
					StartupTime: now,
					IsHealthy:   true,
				},
				{
					PodName:     "redis-0",
					PodUID:      "uid-site1-xyz",
					Namespace:   "redis-site1",
					StartupTime: now,
					IsHealthy:   true,
				},
			},
			expectedPod: "redis-0",
			expectedUID: "uid-site1-xyz", // Lexicographically smaller
		},
		{
			name: "same pod name, different startup times",
			states: []*state.PodState{
				{
					PodName:     "redis-0",
					PodUID:      "uid-site2",
					Namespace:   "redis-site2",
					StartupTime: now.Add(-5 * time.Minute),
					IsHealthy:   true,
				},
				{
					PodName:     "redis-0",
					PodUID:      "uid-site1",
					Namespace:   "redis-site1",
					StartupTime: now.Add(-10 * time.Minute),
					IsHealthy:   true,
				},
			},
			expectedPod: "redis-0",
			expectedUID: "uid-site1", // Older startup time wins
		},
		{
			name: "complex multi-site with 4 pods",
			states: []*state.PodState{
				{
					PodName:     "redis-0",
					PodUID:      "aaa-site1",
					Namespace:   "redis-site1",
					StartupTime: now,
					IsHealthy:   true,
				},
				{
					PodName:     "redis-0",
					PodUID:      "bbb-site2",
					Namespace:   "redis-site2",
					StartupTime: now,
					IsHealthy:   true,
				},
				{
					PodName:     "redis-1",
					PodUID:      "ccc-site1",
					Namespace:   "redis-site1",
					StartupTime: now,
					IsHealthy:   true,
				},
				{
					PodName:     "redis-1",
					PodUID:      "ddd-site2",
					Namespace:   "redis-site2",
					StartupTime: now,
					IsHealthy:   true,
				},
			},
			expectedPod: "redis-0", // Pod name wins first
			expectedUID: "aaa-site1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Sort using multi-site aware logic
			sort.Slice(tt.states, func(i, j int) bool {
				if tt.states[i].StartupTime.Equal(tt.states[j].StartupTime) {
					if tt.states[i].PodName == tt.states[j].PodName {
						return tt.states[i].PodUID < tt.states[j].PodUID
					}
					return tt.states[i].PodName < tt.states[j].PodName
				}
				return tt.states[i].StartupTime.Before(tt.states[j].StartupTime)
			})

			elected := tt.states[0]
			if elected.PodName != tt.expectedPod {
				t.Errorf("Expected pod %s to be elected, got %s", tt.expectedPod, elected.PodName)
			}
			if elected.PodUID != tt.expectedUID {
				t.Errorf("Expected UID %s to be elected, got %s", tt.expectedUID, elected.PodUID)
			}
		})
	}
}

func TestSplitBrainResolution(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		states   []*state.PodState
		expected string
	}{
		{
			name: "two masters, keep oldest",
			states: []*state.PodState{
				{
					PodName:     "redis-0",
					PodUID:      "uid-0",
					StartupTime: now.Add(-10 * time.Minute),
					IsHealthy:   true,
					IsMaster:    true,
				},
				{
					PodName:     "redis-1",
					PodUID:      "uid-1",
					StartupTime: now.Add(-5 * time.Minute),
					IsHealthy:   true,
					IsMaster:    true,
				},
				{
					PodName:     "redis-2",
					PodUID:      "uid-2",
					StartupTime: now.Add(-1 * time.Minute),
					IsHealthy:   true,
					IsMaster:    false,
				},
			},
			expected: "redis-0",
		},
		{
			name: "three masters, keep oldest",
			states: []*state.PodState{
				{
					PodName:     "redis-0",
					PodUID:      "uid-0",
					StartupTime: now.Add(-5 * time.Minute),
					IsHealthy:   true,
					IsMaster:    true,
				},
				{
					PodName:     "redis-1",
					PodUID:      "uid-1",
					StartupTime: now.Add(-10 * time.Minute),
					IsHealthy:   true,
					IsMaster:    true,
				},
				{
					PodName:     "redis-2",
					PodUID:      "uid-2",
					StartupTime: now.Add(-3 * time.Minute),
					IsHealthy:   true,
					IsMaster:    true,
				},
			},
			expected: "redis-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Filter only masters
			var masters []*state.PodState
			for _, state := range tt.states {
				if state.IsMaster {
					masters = append(masters, state)
				}
			}

			// Sort masters by startup time, then pod name, then UID
			sort.Slice(masters, func(i, j int) bool {
				if masters[i].StartupTime.Equal(masters[j].StartupTime) {
					if masters[i].PodName == masters[j].PodName {
						return masters[i].PodUID < masters[j].PodUID
					}
					return masters[i].PodName < masters[j].PodName
				}
				return masters[i].StartupTime.Before(masters[j].StartupTime)
			})

			keepMaster := masters[0]
			if keepMaster.PodName != tt.expected {
				t.Errorf("Expected %s to be kept as master, got %s", tt.expected, keepMaster.PodName)
			}
		})
	}
}

func TestHealthyPodFiltering(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name          string
		states        []*state.PodState
		expectedCount int
		expectedFirst string
	}{
		{
			name: "filter out unhealthy pods",
			states: []*state.PodState{
				{
					PodName:     "redis-0",
					PodUID:      "uid-0",
					StartupTime: now.Add(-10 * time.Minute),
					IsHealthy:   false,
				},
				{
					PodName:     "redis-1",
					PodUID:      "uid-1",
					StartupTime: now.Add(-5 * time.Minute),
					IsHealthy:   true,
				},
				{
					PodName:     "redis-2",
					PodUID:      "uid-2",
					StartupTime: now.Add(-3 * time.Minute),
					IsHealthy:   true,
				},
			},
			expectedCount: 2,
			expectedFirst: "redis-1",
		},
		{
			name: "all healthy",
			states: []*state.PodState{
				{
					PodName:     "redis-0",
					PodUID:      "uid-0",
					StartupTime: now.Add(-10 * time.Minute),
					IsHealthy:   true,
				},
				{
					PodName:     "redis-1",
					PodUID:      "uid-1",
					StartupTime: now.Add(-5 * time.Minute),
					IsHealthy:   true,
				},
			},
			expectedCount: 2,
			expectedFirst: "redis-0",
		},
		{
			name: "all unhealthy",
			states: []*state.PodState{
				{
					PodName:     "redis-0",
					PodUID:      "uid-0",
					StartupTime: now.Add(-10 * time.Minute),
					IsHealthy:   false,
				},
				{
					PodName:     "redis-1",
					PodUID:      "uid-1",
					StartupTime: now.Add(-5 * time.Minute),
					IsHealthy:   false,
				},
			},
			expectedCount: 0,
			expectedFirst: "",
		},
		{
			name: "mixed health, oldest is unhealthy",
			states: []*state.PodState{
				{
					PodName:     "redis-0",
					PodUID:      "uid-0",
					StartupTime: now.Add(-20 * time.Minute),
					IsHealthy:   false,
				},
				{
					PodName:     "redis-1",
					PodUID:      "uid-1",
					StartupTime: now.Add(-10 * time.Minute),
					IsHealthy:   true,
				},
				{
					PodName:     "redis-2",
					PodUID:      "uid-2",
					StartupTime: now.Add(-5 * time.Minute),
					IsHealthy:   false,
				},
			},
			expectedCount: 1,
			expectedFirst: "redis-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Filter healthy pods
			var healthy []*state.PodState
			for _, state := range tt.states {
				if state.IsHealthy {
					healthy = append(healthy, state)
				}
			}

			if len(healthy) != tt.expectedCount {
				t.Errorf("Expected %d healthy pods, got %d", tt.expectedCount, len(healthy))
			}

			if tt.expectedCount > 0 {
				// Sort and check first
				sort.Slice(healthy, func(i, j int) bool {
					if healthy[i].StartupTime.Equal(healthy[j].StartupTime) {
						if healthy[i].PodName == healthy[j].PodName {
							return healthy[i].PodUID < healthy[j].PodUID
						}
						return healthy[i].PodName < healthy[j].PodName
					}
					return healthy[i].StartupTime.Before(healthy[j].StartupTime)
				})

				if healthy[0].PodName != tt.expectedFirst {
					t.Errorf("Expected first healthy pod to be %s, got %s", tt.expectedFirst, healthy[0].PodName)
				}
			}
		})
	}
}

func TestMasterCountScenarios(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name            string
		states          []*state.PodState
		masterCount     int
		needsElection   bool
		needsResolution bool
	}{
		{
			name: "no masters",
			states: []*state.PodState{
				{PodName: "redis-0", PodUID: "uid-0", IsMaster: false, IsHealthy: true, StartupTime: now.Add(-10 * time.Minute)},
				{PodName: "redis-1", PodUID: "uid-1", IsMaster: false, IsHealthy: true, StartupTime: now.Add(-5 * time.Minute)},
			},
			masterCount:     0,
			needsElection:   true,
			needsResolution: false,
		},
		{
			name: "one master",
			states: []*state.PodState{
				{PodName: "redis-0", PodUID: "uid-0", IsMaster: true, IsHealthy: true, StartupTime: now.Add(-10 * time.Minute)},
				{PodName: "redis-1", PodUID: "uid-1", IsMaster: false, IsHealthy: true, StartupTime: now.Add(-5 * time.Minute)},
			},
			masterCount:     1,
			needsElection:   false,
			needsResolution: false,
		},
		{
			name: "multiple masters (split brain)",
			states: []*state.PodState{
				{PodName: "redis-0", PodUID: "uid-0", IsMaster: true, IsHealthy: true, StartupTime: now.Add(-10 * time.Minute)},
				{PodName: "redis-1", PodUID: "uid-1", IsMaster: true, IsHealthy: true, StartupTime: now.Add(-5 * time.Minute)},
			},
			masterCount:     2,
			needsElection:   false,
			needsResolution: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			masterCount := 0
			for _, state := range tt.states {
				if state.IsMaster && state.IsHealthy {
					masterCount++
				}
			}

			if masterCount != tt.masterCount {
				t.Errorf("Expected %d masters, got %d", tt.masterCount, masterCount)
			}

			needsElection := masterCount == 0
			if needsElection != tt.needsElection {
				t.Errorf("Expected needsElection=%v, got %v", tt.needsElection, needsElection)
			}

			needsResolution := masterCount > 1
			if needsResolution != tt.needsResolution {
				t.Errorf("Expected needsResolution=%v, got %v", tt.needsResolution, needsResolution)
			}
		})
	}
}

func TestEvenNumberOfReplicas(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		count    int
		expected string
	}{
		{
			name:     "2 replicas",
			count:    2,
			expected: "redis-0",
		},
		{
			name:     "4 replicas",
			count:    4,
			expected: "redis-0",
		},
		{
			name:     "6 replicas",
			count:    6,
			expected: "redis-0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create N replicas with same startup time
			states := make([]*state.PodState, tt.count)
			for i := 0; i < tt.count; i++ {
				states[i] = &state.PodState{
					PodName:     "redis-" + string(rune('0'+i)),
					PodUID:      "uid-" + string(rune('0'+i)),
					StartupTime: now,
					IsHealthy:   true,
					IsMaster:    false,
				}
			}

			// Sort and elect
			sort.Slice(states, func(i, j int) bool {
				if states[i].StartupTime.Equal(states[j].StartupTime) {
					if states[i].PodName == states[j].PodName {
						return states[i].PodUID < states[j].PodUID
					}
					return states[i].PodName < states[j].PodName
				}
				return states[i].StartupTime.Before(states[j].StartupTime)
			})

			elected := states[0]
			if elected.PodName != tt.expected {
				t.Errorf("Expected %s to be elected with %d replicas, got %s", tt.expected, tt.count, elected.PodName)
			}
		})
	}
}

func TestStaleStateRemoval(t *testing.T) {
	now := time.Now()
	staleThreshold := now.Add(-60 * time.Second)

	states := map[string]*state.PodState{
		"redis-0": {
			PodName:  "redis-0",
			PodUID:   "uid-0",
			LastSeen: now.Add(-30 * time.Second), // Fresh
			IsHealthy: true,
		},
		"redis-1": {
			PodName:  "redis-1",
			PodUID:   "uid-1",
			LastSeen: now.Add(-90 * time.Second), // Stale
			IsHealthy: true,
		},
		"redis-2": {
			PodName:  "redis-2",
			PodUID:   "uid-2",
			LastSeen: now, // Fresh
			IsHealthy: true,
		},
	}

	// Remove stale peers
	for podName, state := range states {
		if state.LastSeen.Before(staleThreshold) {
			delete(states, podName)
		}
	}

	if len(states) != 2 {
		t.Errorf("Expected 2 states after removing stale, got %d", len(states))
	}

	if _, exists := states["redis-1"]; exists {
		t.Error("Expected redis-1 to be removed as stale")
	}

	if _, exists := states["redis-0"]; !exists {
		t.Error("Expected redis-0 to still exist")
	}

	if _, exists := states["redis-2"]; !exists {
		t.Error("Expected redis-2 to still exist")
	}
}
