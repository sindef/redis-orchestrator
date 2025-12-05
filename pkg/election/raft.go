package election

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"io"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
	"github.com/sindef/redis-orchestrator/pkg/auth"
	"github.com/sindef/redis-orchestrator/pkg/orchestrator/state"
	"k8s.io/klog/v2"
)

// RaftStrategy implements leader election using Raft consensus
type RaftStrategy struct {
	localPodName  string
	localPodUID   string
	localPodIP    string
	bindAddr      string
	peers         []string
	dataDir       string
	debug         bool
	bootstrap     bool
	standalone    bool // If true, this is a witness node (non-voting)
	authenticator *auth.Authenticator
	
	raft          *raft.Raft
	mu            sync.RWMutex
	currentLeader *state.PodState
	allStates     map[string]*state.PodState
}

// NewRaftStrategy creates a new Raft-based election strategy
func NewRaftStrategy(podName, podUID, podIP, bindAddr string, peers []string, dataDir string, bootstrap, debug, standalone bool, authenticator *auth.Authenticator) *RaftStrategy {
	return &RaftStrategy{
		localPodName:  podName,
		localPodUID:   podUID,
		localPodIP:    podIP,
		bindAddr:      bindAddr,
		peers:         peers,
		dataDir:       dataDir,
		bootstrap:     bootstrap,
		debug:         debug,
		standalone:    standalone,
		authenticator: authenticator,
		allStates:     make(map[string]*state.PodState),
	}
}

// Start initializes the Raft consensus system
func (r *RaftStrategy) Start(ctx context.Context) error {
	if r.debug {
		klog.InfoS("Starting Raft election strategy (v1.1.0 - auto-discovery)", 
			"bindAddr", r.bindAddr,
			"peers", r.peers,
			"dataDir", r.dataDir,
			"bootstrap", r.bootstrap)
	}

	// Ensure data directory exists
	if err := os.MkdirAll(r.dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create Raft data directory: %w", err)
	}

	// Create Raft configuration
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(r.localPodUID)
	
	if r.debug {
		config.LogLevel = "DEBUG"
	} else {
		config.LogLevel = "INFO"
	}

	// Setup Raft transport
	// Parse bind address to get port
	_, bindPort, err := net.SplitHostPort(r.bindAddr)
	if err != nil {
		return fmt.Errorf("failed to parse bind address: %w", err)
	}

	// Create advertise address using pod IP
	advertiseAddr := net.JoinHostPort(r.localPodIP, bindPort)
	
	if r.debug {
		klog.InfoS("Raft transport configuration",
			"bindAddr", r.bindAddr,
			"advertiseAddr", advertiseAddr)
	}

	// Resolve advertise address
	tcpAddr, err := net.ResolveTCPAddr("tcp", advertiseAddr)
	if err != nil {
		return fmt.Errorf("failed to resolve advertise address: %w", err)
	}

	// Create transport: bind to 0.0.0.0 but advertise pod IP
	transport, err := raft.NewTCPTransport(r.bindAddr, tcpAddr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return fmt.Errorf("failed to create Raft transport: %w", err)
	}

	// Create snapshot store
	snapshots, err := raft.NewFileSnapshotStore(r.dataDir, 2, os.Stderr)
	if err != nil {
		return fmt.Errorf("failed to create snapshot store: %w", err)
	}

	// Create log store
	logStore, err := raftboltdb.NewBoltStore(filepath.Join(r.dataDir, "raft-log.db"))
	if err != nil {
		return fmt.Errorf("failed to create log store: %w", err)
	}

	// Create stable store
	stableStore, err := raftboltdb.NewBoltStore(filepath.Join(r.dataDir, "raft-stable.db"))
	if err != nil {
		return fmt.Errorf("failed to create stable store: %w", err)
	}

	// Create the Raft system
	fsm := &raftFSM{strategy: r}
	ra, err := raft.NewRaft(config, fsm, logStore, stableStore, snapshots, transport)
	if err != nil {
		return fmt.Errorf("failed to create Raft: %w", err)
	}

	r.raft = ra

	// Check if we have existing state
	hasExistingState := ra.LastIndex() > 0
	
	if r.debug {
		klog.InfoS("Raft state check", 
			"hasExistingState", hasExistingState, 
			"lastIndex", ra.LastIndex(),
			"bootstrap", r.bootstrap)
	}

	// Bootstrap cluster only if:
	// 1. No existing state (first run)
	// 2. Bootstrap flag is set (typically only on pod-0)
	// 3. We have peers configured
	// 4. We're not in standalone/witness mode (witness nodes can't bootstrap)
	if !hasExistingState && r.bootstrap && len(r.peers) > 0 && !r.standalone {
		// Get port from bind address for advertise address
		_, port, _ := net.SplitHostPort(r.bindAddr)
		localAdvertise := net.JoinHostPort(r.localPodIP, port)
		
		// Build server list - need to know UIDs of all servers
		// For now, use a simplified approach: bootstrap with just ourselves
		// and let other nodes join via AddVoter (future enhancement)
		configuration := raft.Configuration{
			Servers: []raft.Server{
				{
					ID:       raft.ServerID(r.localPodUID),
					Address:  raft.ServerAddress(localAdvertise),
					Suffrage: raft.Voter,
				},
			},
		}

		if r.debug {
			klog.InfoS("Bootstrapping Raft cluster", 
				"localID", r.localPodUID[:8],
				"localAddress", localAdvertise,
				"servers", len(configuration.Servers))
			klog.Warning("Note: Bootstrap creates single-node cluster. Other nodes will need to join.")
			klog.Warning("For production, manually bootstrap with all servers or use auto-discovery.")
		}

		future := ra.BootstrapCluster(configuration)
		if err := future.Error(); err != nil {
			// Bootstrap may fail if cluster already exists
			if err == raft.ErrCantBootstrap {
				klog.Info("Cluster already bootstrapped")
			} else {
				klog.ErrorS(err, "Bootstrap failed")
			}
		} else {
			klog.Info("✅ Successfully bootstrapped Raft cluster")
		}
	} else {
		if r.debug {
			if hasExistingState {
				klog.Info("Raft has existing state - rejoining cluster")
			} else if !r.bootstrap {
				klog.InfoS("Not bootstrapping - waiting to join cluster", "podName", r.localPodName)
		} else if r.standalone {
			klog.InfoS("Witness mode - will join as non-voting member", "podName", r.localPodName)
		} else {
			klog.Info("No peers configured or already bootstrapped")
		}
	}
}

	if r.debug {
		klog.Info("Raft started successfully")
	}

	// Start monitoring leadership
	go r.monitorLeadership(ctx)

	// If we didn't bootstrap and have no existing state, try to discover and join cluster
	// Witness nodes can also join (as non-voters)
	if !hasExistingState && !r.bootstrap && len(r.peers) > 0 {
		go r.autoJoinCluster(ctx)
	} else if r.standalone && !hasExistingState && len(r.peers) > 0 {
		// Witness nodes that haven't joined yet
		go r.autoJoinCluster(ctx)
	}

	return nil
}

// autoJoinCluster attempts to discover and join an existing Raft cluster
func (r *RaftStrategy) autoJoinCluster(ctx context.Context) {
	// Wait a bit for:
	// 1. HTTP server to start on all pods
	// 2. Bootstrap node to complete bootstrap
	// 3. DNS to propagate
	klog.InfoS("Auto-join: Waiting for cluster to form", "waitTime", "10s")
	time.Sleep(10 * time.Second)

	maxAttempts := 18 // Try for ~90 seconds (5s between attempts)
	for attempt := 0; attempt < maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Check if we've already joined
		leaderAddr, leaderID := r.raft.LeaderWithID()
		if r.raft.State() != raft.Follower || leaderAddr != "" {
			klog.InfoS("Auto-join: Already in cluster", 
				"state", r.raft.State().String(),
				"leaderAddr", leaderAddr,
				"leaderID", leaderID)
			return
		}

		klog.InfoS("Auto-join attempt", 
			"attempt", attempt+1, 
			"of", maxAttempts,
			"currentState", r.raft.State().String())

		// Try to discover existing cluster
		klog.InfoS("Auto-join: Starting discovery", "peers", r.peers)
		clusterInfo, err := r.DiscoverCluster(ctx, r.peers)
		if err != nil {
			klog.InfoS("Discovery attempt failed", 
				"attempt", attempt+1, 
				"error", err.Error(),
				"willRetry", true)
			time.Sleep(5 * time.Second)
			continue
		}

		// Found a cluster! Try to join
		klog.InfoS("Auto-join: Found cluster, requesting join", 
			"leaderAddr", clusterInfo.LeaderAddr,
			"leaderID", clusterInfo.LeaderID[:8],
			"leaderState", clusterInfo.State)

		if err := r.JoinCluster(ctx, clusterInfo.LeaderAddr); err != nil {
			klog.ErrorS(err, "Failed to join cluster", 
				"leader", clusterInfo.LeaderAddr,
				"attempt", attempt+1)
			time.Sleep(5 * time.Second)
			continue
		}

		// Successfully joined!
		klog.InfoS("✅ Successfully auto-joined Raft cluster", 
			"leader", clusterInfo.LeaderAddr,
			"attempt", attempt+1)
		
		// Wait a moment and verify we're in the cluster
		time.Sleep(2 * time.Second)
		newLeaderAddr, newLeaderID := r.raft.LeaderWithID()
		klog.InfoS("Post-join verification",
			"ourState", r.raft.State().String(),
			"leaderAddr", newLeaderAddr,
			"leaderID", newLeaderID)
		return
	}

	klog.Warning("⚠️ Failed to auto-join Raft cluster after all attempts - will continue as standalone")
	klog.Warning("This node will participate in elections but not join existing cluster")
}

// Stop gracefully stops the Raft system
func (r *RaftStrategy) Stop() error {
	if r.raft != nil {
		if r.debug {
			klog.Info("Shutting down Raft")
		}
		return r.raft.Shutdown().Error()
	}
	return nil
}

// IsLeader returns true if this instance is the current Raft leader
func (r *RaftStrategy) IsLeader() bool {
	if r.raft == nil {
		return false
	}
	return r.raft.State() == raft.Leader
}

// GetLeader returns the current leader
func (r *RaftStrategy) GetLeader() (string, string, error) {
	if r.raft == nil {
		return "", "", fmt.Errorf("Raft not initialized")
	}

	leaderAddr, leaderID := r.raft.LeaderWithID()
	if leaderAddr == "" {
		return "", "", fmt.Errorf("no Raft leader elected")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	// Try to find the leader in our state map
	for _, st := range r.allStates {
		if st.PodUID == string(leaderID) {
			return st.PodName, st.PodUID, nil
		}
	}

	// Return the Raft leader ID if we don't have pod info
	return string(leaderID), string(leaderID), nil
}

// ElectLeader with Raft doesn't do election per call - it uses continuous consensus
// This method updates our view of the cluster state
func (r *RaftStrategy) ElectLeader(ctx context.Context, allStates []*state.PodState, localState *state.PodState) (*state.PodState, error) {
	// Update our internal state map
	r.mu.Lock()
	for _, st := range allStates {
		r.allStates[st.PodUID] = st
	}
	r.mu.Unlock()

	// The leader is determined by Raft, not by us
	// Get the current Raft leader
	leaderAddr, leaderID := r.raft.LeaderWithID()
	
	if leaderAddr == "" {
		if r.debug {
			klog.Warning("No Raft leader currently elected")
		}
		return nil, fmt.Errorf("no Raft leader")
	}

	// Find the leader state
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	for _, st := range allStates {
		if st.PodUID == string(leaderID) {
			if r.debug {
				klog.InfoS("Raft leader identified",
					"pod", st.PodName,
					"uid", st.PodUID,
					"address", leaderAddr)
			}
			r.currentLeader = st
			return st, nil
		}
	}

	// If we can't find the leader in our states, use local as fallback if we're the leader
	if r.IsLeader() {
		if r.debug {
			klog.Info("We are the Raft leader")
		}
		r.currentLeader = localState
		return localState, nil
	}

	return nil, fmt.Errorf("Raft leader not found in pod states")
}

// Name returns the strategy name
func (r *RaftStrategy) Name() string {
	return "raft"
}

// monitorLeadership watches for leadership changes
func (r *RaftStrategy) monitorLeadership(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if r.debug {
				state := r.raft.State()
				leaderAddr, leaderID := r.raft.LeaderWithID()
				klog.InfoS("Raft state",
					"state", state.String(),
					"leaderAddr", leaderAddr,
					"leaderID", leaderID,
					"isLeader", r.IsLeader())
			}
		}
	}
}

// raftFSM implements the Raft finite state machine
type raftFSM struct {
	strategy *RaftStrategy
}

func (f *raftFSM) Apply(log *raft.Log) interface{} {
	// For now, we don't need to apply any commands
	// The leadership itself is what we care about
	return nil
}

func (f *raftFSM) Snapshot() (raft.FSMSnapshot, error) {
	// No state to snapshot for now
	return &raftFSMSnapshot{}, nil
}

func (f *raftFSM) Restore(snapshot io.ReadCloser) error {
	// No state to restore for now
	return nil
}

type raftFSMSnapshot struct{}

func (f *raftFSMSnapshot) Persist(sink raft.SnapshotSink) error {
	return sink.Cancel()
}

func (f *raftFSMSnapshot) Release() {}

