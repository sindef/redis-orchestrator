package election

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"k8s.io/klog/v2"
)

type RaftClusterInfo struct {
	LeaderAddr string   `json:"leader_addr"`
	LeaderID   string   `json:"leader_id"`
	State      string   `json:"state"`
	PodName    string   `json:"pod_name"`
	PodUID     string   `json:"pod_uid"`
	Peers      []string `json:"peers"`
}

// DiscoverCluster queries peers to find an existing Raft cluster and its leader.
// Returns cluster info if found, allowing new nodes to join without manual configuration.
func (r *RaftStrategy) DiscoverCluster(ctx context.Context, peerAddrs []string) (*RaftClusterInfo, error) {
	klog.InfoS("Discovery: Looking for existing Raft cluster", 
		"peerCount", len(peerAddrs),
		"peers", peerAddrs)

	// Try each peer until we find one that responds with cluster information.
	for _, peerAddr := range peerAddrs {
		host, _, err := splitHostPort(peerAddr)
		if err != nil {
			klog.InfoS("Discovery: Failed to parse peer address", "peer", peerAddr, "error", err)
			continue
		}
		
		klog.V(2).InfoS("Discovery: Querying peer", "peer", peerAddr, "host", host)

		url := fmt.Sprintf("http://%s:8080/raft/status", host)
		
		reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
		if err != nil {
			continue
		}

		if r.authenticator != nil {
			if err := r.authenticator.SignRequest(req); err != nil {
				klog.InfoS("Discovery: Failed to sign request", "peer", peerAddr, "error", err)
				continue
			}
		}

		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			klog.InfoS("Discovery: Failed to query peer", 
				"peer", peerAddr, 
				"host", host,
				"url", url,
				"error", err.Error())
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			klog.InfoS("Discovery: Peer returned non-OK status",
				"peer", peerAddr,
				"status", resp.StatusCode)
			continue
		}

		var info RaftClusterInfo
		if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
			klog.InfoS("Discovery: Failed to decode response", 
				"peer", peerAddr, 
				"error", err.Error())
			continue
		}

		klog.InfoS("Discovery: Peer responded",
			"peer", peerAddr,
			"peerState", info.State,
			"peerLeader", info.LeaderAddr,
			"peerID", info.PodUID)

		// If peer reports a leader, we found the cluster.
		if info.LeaderAddr != "" {
			klog.InfoS("✅ Discovery: Found existing Raft cluster",
				"queriedPeer", peerAddr,
				"leaderAddr", info.LeaderAddr,
				"leaderState", info.State,
				"leaderPod", info.PodName)
			return &info, nil
		}

		// If the queried peer is itself the leader, use its address.
		if info.State == "Leader" {
			info.LeaderAddr = peerAddr
			klog.InfoS("✅ Discovery: Found leader directly",
				"peer", peerAddr,
				"leaderID", info.LeaderID,
				"leaderPod", info.PodName)
			return &info, nil
		}
	}

	klog.Warning("Discovery: No existing Raft cluster found after querying all peers")
	return nil, fmt.Errorf("no existing Raft cluster found")
}

// JoinCluster requests membership in the Raft cluster by calling the leader's add endpoint.
// Standalone nodes join as non-voters (witnesses), regular nodes join as voters.
func (r *RaftStrategy) JoinCluster(ctx context.Context, leaderAddr string) error {
	if r.raft == nil {
		return fmt.Errorf("Raft not initialized")
	}

	// Advertise address must use pod IP, not bind address, so leader can reach us.
	_, port, _ := splitHostPort(r.bindAddr)
	localAdvertise := fmt.Sprintf("%s:%s", r.localPodIP, port)

	if r.debug {
		klog.InfoS("Attempting to join Raft cluster",
			"leader", leaderAddr,
			"ourID", r.localPodUID[:8],
			"ourAddr", localAdvertise)
	}

	// Choose endpoint based on node type: witnesses don't vote but help with quorum.
	var endpoint string
	if r.standalone {
		endpoint = "/raft/add-nonvoter"
		if r.debug {
			klog.InfoS("Joining as witness (non-voting member)", "leader", leaderAddr)
		}
	} else {
		endpoint = "/raft/add-voter"
	}
	url := fmt.Sprintf("http://%s:8080%s", strings.Split(leaderAddr, ":")[0], endpoint)
	
	requestBody := map[string]string{
		"id":      r.localPodUID,
		"address": localAdvertise,
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return fmt.Errorf("failed to marshal join request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(bodyBytes)))
	if err != nil {
		return fmt.Errorf("failed to create join request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	if r.authenticator != nil {
		if err := r.authenticator.SignRequest(req); err != nil {
			return fmt.Errorf("failed to sign join request: %w", err)
		}
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to contact leader: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("join request failed with status %d", resp.StatusCode)
	}

	klog.InfoS("Successfully joined Raft cluster", "leader", leaderAddr)
	return nil
}

// splitHostPort parses an address, defaulting to port 7000 if not specified.
// This handles both "host:port" and "host" formats for peer addresses.
func splitHostPort(addr string) (host, port string, err error) {
	if !strings.Contains(addr, ":") {
		return addr, "7000", nil
	}
	
	host, port, err = net.SplitHostPort(addr)
	if err != nil {
		return "", "", err
	}
	return host, port, nil
}

