package election

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/raft"
	"k8s.io/klog/v2"
)

// AddVoterRequest represents a request to add a voter to the Raft cluster
type AddVoterRequest struct {
	ID      string `json:"id"`
	Address string `json:"address"`
}

// HandleRaftStatus returns the current Raft status
func (r *RaftStrategy) HandleRaftStatus(w http.ResponseWriter, req *http.Request) {
	if r.raft == nil {
		http.Error(w, "Raft not initialized", http.StatusServiceUnavailable)
		return
	}

	leaderAddr, leaderID := r.raft.LeaderWithID()
	state := r.raft.State()

	info := RaftClusterInfo{
		LeaderAddr: string(leaderAddr),
		LeaderID:   string(leaderID),
		State:      state.String(),
		PodName:    r.localPodName,
		PodUID:     r.localPodUID,
		Peers:      r.peers,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
}

// HandleAddVoter handles requests to add a new voter to the Raft cluster
func (r *RaftStrategy) HandleAddVoter(w http.ResponseWriter, req *http.Request) {
	if r.raft == nil {
		http.Error(w, "Raft not initialized", http.StatusServiceUnavailable)
		return
	}

	// Only the leader can add voters
	if r.raft.State() != raft.Leader {
		leaderAddr, _ := r.raft.LeaderWithID()
		http.Error(w, fmt.Sprintf("Not the leader, leader is: %s", leaderAddr), http.StatusBadRequest)
		return
	}

	var request AddVoterRequest
	if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if request.ID == "" || request.Address == "" {
		http.Error(w, "ID and Address are required", http.StatusBadRequest)
		return
	}

	if r.debug {
		klog.InfoS("Received AddVoter request",
			"id", request.ID[:8],
			"address", request.Address)
	}

	// Check if server is already in the cluster
	configFuture := r.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to get configuration: %v", err), http.StatusInternalServerError)
		return
	}

	for _, server := range configFuture.Configuration().Servers {
		if server.ID == raft.ServerID(request.ID) {
			if r.debug {
				klog.InfoS("Server already in cluster", "id", request.ID[:8])
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "already_member"})
			return
		}
	}

	// Add the voter
	future := r.raft.AddVoter(
		raft.ServerID(request.ID),
		raft.ServerAddress(request.Address),
		0, // prevIndex
		10*time.Second,
	)

	if err := future.Error(); err != nil {
		klog.ErrorS(err, "Failed to add voter", "id", request.ID[:8], "address", request.Address)
		http.Error(w, fmt.Sprintf("Failed to add voter: %v", err), http.StatusInternalServerError)
		return
	}

	klog.InfoS("Successfully added voter to Raft cluster",
		"id", request.ID[:8],
		"address", request.Address)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "added"})
}

// HandleAddNonvoter handles requests to add a new non-voting member (witness) to the Raft cluster
func (r *RaftStrategy) HandleAddNonvoter(w http.ResponseWriter, req *http.Request) {
	if r.raft == nil {
		http.Error(w, "Raft not initialized", http.StatusServiceUnavailable)
		return
	}

	// Only the leader can add non-voters
	if r.raft.State() != raft.Leader {
		leaderAddr, _ := r.raft.LeaderWithID()
		http.Error(w, fmt.Sprintf("Not the leader, leader is: %s", leaderAddr), http.StatusBadRequest)
		return
	}

	var request AddVoterRequest
	if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if request.ID == "" || request.Address == "" {
		http.Error(w, "ID and Address are required", http.StatusBadRequest)
		return
	}

	if r.debug {
		klog.InfoS("Received AddNonvoter request (witness)",
			"id", request.ID[:8],
			"address", request.Address)
	}

	// Check if server is already in the cluster
	configFuture := r.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to get configuration: %v", err), http.StatusInternalServerError)
		return
	}

	for _, server := range configFuture.Configuration().Servers {
		if server.ID == raft.ServerID(request.ID) {
			if r.debug {
				klog.InfoS("Server already in cluster", "id", request.ID[:8])
			}
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "already_member"})
			return
		}
	}

	// Add as non-voter (witness can vote but cannot become leader)
	future := r.raft.AddNonvoter(
		raft.ServerID(request.ID),
		raft.ServerAddress(request.Address),
		0, // prevIndex
		10*time.Second,
	)

	if err := future.Error(); err != nil {
		klog.ErrorS(err, "Failed to add non-voter", "id", request.ID[:8], "address", request.Address)
		http.Error(w, fmt.Sprintf("Failed to add non-voter: %v", err), http.StatusInternalServerError)
		return
	}

	klog.InfoS("Successfully added non-voter (witness) to Raft cluster",
		"id", request.ID[:8],
		"address", request.Address)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "added"})
}

// HandleRaftPeers returns the current Raft peer configuration
func (r *RaftStrategy) HandleRaftPeers(w http.ResponseWriter, req *http.Request) {
	if r.raft == nil {
		http.Error(w, "Raft not initialized", http.StatusServiceUnavailable)
		return
	}

	configFuture := r.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to get configuration: %v", err), http.StatusInternalServerError)
		return
	}

	servers := make([]map[string]string, 0)
	for _, server := range configFuture.Configuration().Servers {
		servers = append(servers, map[string]string{
			"id":       string(server.ID),
			"address":  string(server.Address),
			"suffrage": server.Suffrage.String(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"servers": servers,
		"count":   len(servers),
	})
}

