package election

import (
	"strings"
	"testing"
)

func TestSplitHostPort(t *testing.T) {
	tests := []struct {
		name         string
		addr         string
		expectedHost string
		expectedPort string
		expectError  bool
	}{
		{
			name:         "standard host:port",
			addr:         "redis-0.redis-headless:7000",
			expectedHost: "redis-0.redis-headless",
			expectedPort: "7000",
			expectError:  false,
		},
		{
			name:         "IP:port",
			addr:         "10.0.1.5:7000",
			expectedHost: "10.0.1.5",
			expectedPort: "7000",
			expectError:  false,
		},
		{
			name:         "hostname only (no port)",
			addr:         "redis-0",
			expectedHost: "redis-0",
			expectedPort: "7000", // Default
			expectError:  false,
		},
		{
			name:         "FQDN",
			addr:         "redis-0.redis-headless.redis-raft.svc.cluster.local:7000",
			expectedHost: "redis-0.redis-headless.redis-raft.svc.cluster.local",
			expectedPort: "7000",
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, err := splitHostPort(tt.addr)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}

			if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if host != tt.expectedHost {
				t.Errorf("Expected host %s, got %s", tt.expectedHost, host)
			}

			if port != tt.expectedPort {
				t.Errorf("Expected port %s, got %s", tt.expectedPort, port)
			}
		})
	}
}

func TestBootstrapDetection(t *testing.T) {
	tests := []struct {
		name              string
		podName           string
		shouldBootstrap   bool
	}{
		{
			name:            "StatefulSet pod-0",
			podName:         "redis-0",
			shouldBootstrap: true,
		},
		{
			name:            "StatefulSet pod-1",
			podName:         "redis-1",
			shouldBootstrap: false,
		},
		{
			name:            "StatefulSet pod-2",
			podName:         "redis-2",
			shouldBootstrap: false,
		},
		{
			name:            "Different naming ending in -0",
			podName:         "myredis-0",
			shouldBootstrap: true,
		},
		{
			name:            "Deployment style name",
			podName:         "redis-abc123",
			shouldBootstrap: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shouldBootstrap := strings.HasSuffix(tt.podName, "-0")

			if shouldBootstrap != tt.shouldBootstrap {
				t.Errorf("Expected shouldBootstrap=%v for pod %s, got %v",
					tt.shouldBootstrap, tt.podName, shouldBootstrap)
			}
		})
	}
}

func TestRaftClusterInfoParsing(t *testing.T) {
	info := RaftClusterInfo{
		LeaderAddr: "10.0.1.5:7000",
		LeaderID:   "abc-123-def",
		State:      "Follower",
		PodName:    "redis-1",
		PodUID:     "uid-123",
		Peers:      []string{"redis-0:7000", "redis-1:7000"},
	}

	if info.LeaderAddr == "" {
		t.Error("LeaderAddr should not be empty")
	}

	if info.State != "Follower" {
		t.Errorf("Expected state Follower, got %s", info.State)
	}

	if len(info.Peers) != 2 {
		t.Errorf("Expected 2 peers, got %d", len(info.Peers))
	}
}

func TestAddVoterRequest(t *testing.T) {
	req := AddVoterRequest{
		ID:      "test-uid-12345",
		Address: "10.0.1.6:7000",
	}

	if req.ID == "" {
		t.Error("ID should not be empty")
	}

	if req.Address == "" {
		t.Error("Address should not be empty")
	}

	if req.ID == "" || req.Address == "" {
		t.Error("Request should be valid")
	}
}

