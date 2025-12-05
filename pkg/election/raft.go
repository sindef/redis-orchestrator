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

type RaftStrategy struct {
	localPodName  string
	localPodUID   string
	localPodIP    string
	bindAddr      string
	peers         []string
	dataDir       string
	debug         bool
	bootstrap     bool
	standalone    bool
	authenticator *auth.Authenticator
	
	raft          *raft.Raft
	mu            sync.RWMutex
	currentLeader *state.PodState
	allStates     map[string]*state.PodState
}

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

func (r *RaftStrategy) Start(ctx context.Context) error {
	if r.debug {
		klog.InfoS("Starting Raft election strategy (v1.1.0 - auto-discovery)", 
			"bindAddr", r.bindAddr,
			"peers", r.peers,
			"dataDir", r.dataDir,
			"bootstrap", r.bootstrap)
	}

	if err := os.MkdirAll(r.dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create Raft data directory: %w", err)
	}

	// Use pod UID as Raft server ID to ensure uniqueness even if pods are recreated
	// with the same name. This prevents ID conflicts during pod restarts.
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(r.localPodUID)
	
	if r.debug {
		config.LogLevel = "DEBUG"
	} else {
		config.LogLevel = "INFO"
	}

	// Advertise address uses pod IP (not bind address) so other pods can reach us.
	// Bind address may be 0.0.0.0, but peers need the actual pod IP for connections.
	_, bindPort, err := net.SplitHostPort(r.bindAddr)
	if err != nil {
		return fmt.Errorf("failed to parse bind address: %w", err)
	}

	advertiseAddr := net.JoinHostPort(r.localPodIP, bindPort)
	
	if r.debug {
		klog.InfoS("Raft transport configuration",
			"bindAddr", r.bindAddr,
			"advertiseAddr", advertiseAddr)
	}

	tcpAddr, err := net.ResolveTCPAddr("tcp", advertiseAddr)
	if err != nil {
		return fmt.Errorf("failed to resolve advertise address: %w", err)
	}

	transport, err := raft.NewTCPTransport(r.bindAddr, tcpAddr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return fmt.Errorf("failed to create Raft transport: %w", err)
	}

	snapshots, err := raft.NewFileSnapshotStore(r.dataDir, 2, os.Stderr)
	if err != nil {
		return fmt.Errorf("failed to create snapshot store: %w", err)
	}

	logStore, err := raftboltdb.NewBoltStore(filepath.Join(r.dataDir, "raft-log.db"))
	if err != nil {
		return fmt.Errorf("failed to create log store: %w", err)
	}

	stableStore, err := raftboltdb.NewBoltStore(filepath.Join(r.dataDir, "raft-stable.db"))
	if err != nil {
		return fmt.Errorf("failed to create stable store: %w", err)
	}

	fsm := &raftFSM{strategy: r}
	ra, err := raft.NewRaft(config, fsm, logStore, stableStore, snapshots, transport)
	if err != nil {
		return fmt.Errorf("failed to create Raft: %w", err)
	}

	r.raft = ra

	// Check for existing Raft state to determine if this is a new node or rejoining.
	// LastIndex > 0 indicates the node has participated in a cluster before.
	hasExistingState := ra.LastIndex() > 0
	
	if r.debug {
		klog.InfoS("Raft state check", 
			"hasExistingState", hasExistingState, 
			"lastIndex", ra.LastIndex(),
			"bootstrap", r.bootstrap)
	}

	// Bootstrap only if: no existing state, bootstrap flag set, peers configured, and not standalone.
	// Standalone nodes join as non-voters, so they shouldn't bootstrap.
	if !hasExistingState && r.bootstrap && len(r.peers) > 0 && !r.standalone {
		_, port, _ := net.SplitHostPort(r.bindAddr)
		localAdvertise := net.JoinHostPort(r.localPodIP, port)
		
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

		// Bootstrap creates a single-node cluster. Other nodes will join via auto-join
		// or manual API calls. ErrCantBootstrap is expected if state already exists.
		future := ra.BootstrapCluster(configuration)
		if err := future.Error(); err != nil {
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

	// Monitor leadership changes for debugging and state tracking.
	go r.monitorLeadership(ctx)

	// Auto-join attempts to discover and join an existing cluster if:
	// - No existing state (new node)
	// - Not bootstrapping (would create new cluster)
	// - Peers configured (knows where to look)
	// Standalone nodes also auto-join but as non-voters.
	if !hasExistingState && !r.bootstrap && len(r.peers) > 0 {
		go r.autoJoinCluster(ctx)
	} else if r.standalone && !hasExistingState && len(r.peers) > 0 {
		go r.autoJoinCluster(ctx)
	}

	return nil
}

// autoJoinCluster attempts to discover and join an existing Raft cluster.
// It retries with exponential backoff to handle cases where the cluster is still forming.
func (r *RaftStrategy) autoJoinCluster(ctx context.Context) {
	// Initial delay allows bootstrap node to start and become leader.
	klog.InfoS("Auto-join: Waiting for cluster to form", "waitTime", "10s")
	time.Sleep(10 * time.Second)

	maxAttempts := 18
	for attempt := 0; attempt < maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Check if we've already joined (have a leader or are not a follower).
		// This handles race conditions where another process joined us.
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

		klog.InfoS("✅ Successfully auto-joined Raft cluster", 
			"leader", clusterInfo.LeaderAddr,
			"attempt", attempt+1)
		
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

func (r *RaftStrategy) Stop() error {
	if r.raft != nil {
		if r.debug {
			klog.Info("Shutting down Raft")
		}
		return r.raft.Shutdown().Error()
	}
	return nil
}

func (r *RaftStrategy) IsLeader() bool {
	if r.raft == nil {
		return false
	}
	return r.raft.State() == raft.Leader
}

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

	for _, st := range r.allStates {
		if st.PodUID == string(leaderID) {
			return st.PodName, st.PodUID, nil
		}
	}

	return string(leaderID), string(leaderID), nil
}

// ElectLeader returns the current Raft leader by matching leader ID to pod states.
// This bridges Raft's internal leader election to the orchestrator's pod-based view.
func (r *RaftStrategy) ElectLeader(ctx context.Context, allStates []*state.PodState, localState *state.PodState) (*state.PodState, error) {
	// Update internal state map for leader lookup.
	r.mu.Lock()
	for _, st := range allStates {
		r.allStates[st.PodUID] = st
	}
	r.mu.Unlock()

	leaderAddr, leaderID := r.raft.LeaderWithID()
	
	// No leader means cluster is still electing or partitioned.
	if leaderAddr == "" {
		if r.debug {
			klog.Warning("No Raft leader currently elected")
		}
		return nil, fmt.Errorf("no Raft leader")
	}

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

	if r.IsLeader() {
		if r.debug {
			klog.Info("We are the Raft leader")
		}
		r.currentLeader = localState
		return localState, nil
	}

	return nil, fmt.Errorf("Raft leader not found in pod states")
}

func (r *RaftStrategy) Name() string {
	return "raft"
}

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

// raftFSM is a minimal Raft FSM. We only use Raft for leader election,
// not for state machine replication, so these methods are no-ops.
// The FSM is required by the Raft library but not used for our use case.
type raftFSM struct {
	strategy *RaftStrategy
}

func (f *raftFSM) Apply(log *raft.Log) interface{} {
	return nil
}

func (f *raftFSM) Snapshot() (raft.FSMSnapshot, error) {
	return &raftFSMSnapshot{}, nil
}

func (f *raftFSM) Restore(snapshot io.ReadCloser) error {
	return nil
}

type raftFSMSnapshot struct{}

// Persist cancels immediately since we don't need to persist state.
// This is safe because we only use Raft for leadership, not state replication.
func (f *raftFSMSnapshot) Persist(sink raft.SnapshotSink) error {
	return sink.Cancel()
}

func (f *raftFSMSnapshot) Release() {}

