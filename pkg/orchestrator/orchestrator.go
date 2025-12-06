package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/sindef/redis-orchestrator/pkg/auth"
	"github.com/sindef/redis-orchestrator/pkg/config"
	"github.com/sindef/redis-orchestrator/pkg/election"
	"github.com/sindef/redis-orchestrator/pkg/orchestrator/state"
	redisclient "github.com/sindef/redis-orchestrator/pkg/redis"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	// MasterLabel is applied to the Kubernetes pod to indicate it's the Redis master.
	// This enables service discovery and load balancing to route writes to the master.
	MasterLabel      = "redis-role"
	MasterLabelValue = "master"
	HTTPPort         = 8080
)

type Orchestrator struct {
	config      *config.Config
	kubeClient  kubernetes.Interface
	redisClient *redisclient.Client

	startupTime time.Time
	podIP       string
	podUID      string

	electionStrategy election.Strategy

	authenticator *auth.Authenticator

	mu         sync.RWMutex
	peerStates map[string]*state.PodState

	httpServer *http.Server
}

func New(cfg *config.Config, kubeClient kubernetes.Interface) (*Orchestrator, error) {
	// In standalone mode, we don't manage Redis, so no client is needed.
	// The node still participates in Raft elections to maintain quorum.
	var redisClient *redisclient.Client
	if !cfg.Standalone {
		var err error
		redisClient, err = redisclient.NewClient(
			cfg.RedisHost,
			cfg.RedisPort,
			cfg.RedisPassword,
			cfg.RedisTLS,
			cfg.RedisTLSSkipVerify,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create Redis client: %w", err)
		}
	} else {
		klog.Info("Standalone mode: skipping Redis client creation")
	}

	pod, err := kubeClient.CoreV1().Pods(cfg.Namespace).Get(
		context.Background(),
		cfg.PodName,
		metav1.GetOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get pod: %w", err)
	}

	// Determine the node IP for Raft: use LoadBalancer external IP if configured,
	// otherwise fall back to pod IP.
	nodeIP := pod.Status.PodIP
	if cfg.DiscoverLBService {
		lbIP, svcName, err := discoverLoadBalancerIP(kubeClient, cfg.Namespace, cfg.PodName)
		if err != nil {
			return nil, fmt.Errorf("failed to discover LoadBalancer IP: %w", err)
		}
		if lbIP != "" {
			nodeIP = lbIP
			klog.InfoS("Using LoadBalancer external IP for Raft node",
				"service", svcName,
				"externalIP", lbIP,
				"podIP", pod.Status.PodIP)
		} else if svcName != "" {
			klog.InfoS("LoadBalancer service found but no external IP yet, using pod IP",
				"service", svcName,
				"podIP", pod.Status.PodIP)
		}
	}

	authenticator := auth.New(cfg.SharedSecret)

	var electionStrategy election.Strategy
	switch cfg.ElectionMode {
	case config.ElectionModeDeterministic, "":
		electionStrategy = election.NewDeterministicStrategy(
			cfg.PodName,
			string(pod.UID),
			cfg.Debug,
		)
		klog.Info("Using deterministic election strategy")
	case config.ElectionModeRaft:
		electionStrategy = election.NewRaftStrategy(
			cfg.PodName,
			string(pod.UID),
			nodeIP,
			cfg.RaftBindAddr,
			cfg.RaftPeers,
			cfg.RaftDataDir,
			cfg.RaftBootstrap,
			cfg.Debug,
			cfg.Standalone,
			authenticator,
		)
		klog.InfoS("Using Raft election strategy",
			"nodeIP", nodeIP,
			"podIP", pod.Status.PodIP,
			"bindAddr", cfg.RaftBindAddr,
			"peers", cfg.RaftPeers,
			"bootstrap", cfg.RaftBootstrap,
			"discoverLBService", cfg.DiscoverLBService)
	default:
		return nil, fmt.Errorf("unknown election mode: %s", cfg.ElectionMode)
	}

	o := &Orchestrator{
		config:           cfg,
		kubeClient:       kubeClient,
		redisClient:      redisClient,
		startupTime:      time.Now(),
		podIP:            pod.Status.PodIP,
		podUID:           string(pod.UID),
		electionStrategy: electionStrategy,
		authenticator:    authenticator,
		peerStates:       make(map[string]*state.PodState),
	}

	o.setupHTTPServer()

	return o, nil
}

func (o *Orchestrator) Run(ctx context.Context) error {
	klog.InfoS("Starting orchestrator",
		"pod", o.config.PodName,
		"startupTime", o.startupTime,
		"electionMode", o.config.ElectionMode)

	if err := o.electionStrategy.Start(ctx); err != nil {
		return fmt.Errorf("failed to start election strategy: %w", err)
	}

	go func() {
		klog.InfoS("Starting HTTP server", "port", HTTPPort)
		if err := o.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			klog.ErrorS(err, "HTTP server error")
		}
	}()

	// Periodic state synchronization: discover peers, check Redis roles, and reconcile.
	// Initial sync runs immediately to establish state before the first tick.
	ticker := time.NewTicker(o.config.SyncInterval)
	defer ticker.Stop()

	if err := o.syncState(ctx); err != nil {
		klog.ErrorS(err, "Initial sync failed")
	}

	for {
		select {
		case <-ctx.Done():
			klog.Info("Context cancelled, shutting down")
			return o.shutdown()
		case <-ticker.C:
			if err := o.syncState(ctx); err != nil {
				klog.ErrorS(err, "Sync failed")
			}
		}
	}
}

func (o *Orchestrator) syncState(ctx context.Context) error {
	if o.config.Debug {
		klog.Info("============================================")
		klog.InfoS("Starting state sync cycle", "pod", o.config.PodName, "namespace", o.config.Namespace)
	} else {
		klog.V(2).Info("Starting state sync")
	}

	localState, err := o.getLocalState(ctx)
	if err != nil {
		return fmt.Errorf("failed to get local state: %w", err)
	}

	if o.config.Debug {
		klog.InfoS("Local Redis state",
			"pod", localState.PodName,
			"uid", localState.PodUID,
			"namespace", localState.Namespace,
			"isMaster", localState.IsMaster,
			"isHealthy", localState.IsHealthy,
			"startupTime", localState.StartupTime.Format(time.RFC3339))
	}

	peerStates, err := o.discoverAndQueryPeers(ctx)
	if err != nil {
		klog.ErrorS(err, "Failed to discover peers")
	}

	if o.config.Debug {
		klog.InfoS("Discovered peers", "count", len(peerStates))
		for podName, state := range peerStates {
			klog.InfoS("Peer state",
				"pod", podName,
				"uid", state.PodUID,
				"namespace", state.Namespace,
				"isMaster", state.IsMaster,
				"isHealthy", state.IsHealthy,
				"startupTime", state.StartupTime.Format(time.RFC3339))
		}
	}

	o.updatePeerStates(localState, peerStates)

	return o.reconcile(ctx, localState)
}

func (o *Orchestrator) getLocalState(ctx context.Context) (*state.PodState, error) {
	if o.config.Standalone {
		st := &state.PodState{
			PodName:     o.config.PodName,
			PodIP:       o.podIP,
			PodUID:      o.podUID,
			Namespace:   o.config.Namespace,
			IsMaster:    false,
			IsHealthy:   true,
			IsWitness:   true,
			StartupTime: o.startupTime,
			LastSeen:    time.Now(),
		}
		klog.V(2).Info("Standalone witness state")
		return st, nil
	}

	info, err := o.redisClient.GetReplicationInfo(ctx)
	if err != nil {
		return nil, err
	}

	st := &state.PodState{
		PodName:     o.config.PodName,
		PodIP:       o.podIP,
		PodUID:      o.podUID,
		Namespace:   o.config.Namespace,
		IsMaster:    info.Role == "master",
		IsHealthy:   o.redisClient.IsHealthy(ctx),
		IsWitness:   false,
		StartupTime: o.startupTime,
		LastSeen:    time.Now(),
	}

	klog.V(2).InfoS("Local state", "isMaster", st.IsMaster, "isHealthy", st.IsHealthy)

	return st, nil
}

func (o *Orchestrator) discoverAndQueryPeers(ctx context.Context) (map[string]*state.PodState, error) {
	pods, err := o.kubeClient.CoreV1().Pods(o.config.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: o.config.LabelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	states := make(map[string]*state.PodState)

	for _, pod := range pods.Items {
		if pod.Name == o.config.PodName {
			continue
		}

		// Only query pods that are actually running to avoid querying
		// pods that are starting up, terminating, or in error states.
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}

		state, err := o.queryPeer(ctx, pod.Status.PodIP)
		if err != nil {
			klog.V(2).InfoS("Failed to query peer", "pod", pod.Name, "error", err)
			continue
		}

		states[pod.Name] = state
	}

	klog.V(2).InfoS("Discovered peers", "count", len(states))

	return states, nil
}

func (o *Orchestrator) queryPeer(ctx context.Context, peerIP string) (*state.PodState, error) {
	url := fmt.Sprintf("http://%s:%d/state", peerIP, HTTPPort)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	if err := o.authenticator.SignRequest(req); err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}

	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var st state.PodState
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return nil, err
	}

	return &st, nil
}

func (o *Orchestrator) updatePeerStates(localState *state.PodState, peerStates map[string]*state.PodState) {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Update peer states: include local state and merge discovered peers.
	// Remove stale entries (not seen in 60s) to handle pod deletions gracefully.
	o.peerStates[o.config.PodName] = localState

	for podName, state := range peerStates {
		o.peerStates[podName] = state
	}

	staleThreshold := time.Now().Add(-60 * time.Second)
	for podName, state := range o.peerStates {
		if podName != o.config.PodName && state.LastSeen.Before(staleThreshold) {
			klog.InfoS("Removing stale peer", "pod", podName)
			delete(o.peerStates, podName)
		}
	}
}

// reconcile ensures exactly one Redis master exists among healthy pods.
// It handles four scenarios: no master, single master, multiple masters (split-brain), and Raft mode.
func (o *Orchestrator) reconcile(ctx context.Context, localState *state.PodState) error {
	// Filter to only healthy pods - unhealthy pods can't be masters or reliable replicas.
	o.mu.RLock()
	allStates := make([]*state.PodState, 0, len(o.peerStates))
	for _, st := range o.peerStates {
		if st.IsHealthy {
			allStates = append(allStates, st)
		}
	}
	o.mu.RUnlock()

	if len(allStates) == 0 {
		klog.Warning("No healthy pods found including self")
		return nil
	}

	masterCount := 0
	var currentMaster *state.PodState
	var masters []*state.PodState
	for _, st := range allStates {
		if st.IsMaster {
			masterCount++
			currentMaster = st
			masters = append(masters, st)
		}
	}

	if o.config.Debug {
		klog.InfoS("Reconciliation analysis",
			"totalHealthyPods", len(allStates),
			"masterCount", masterCount)

		if masterCount > 0 {
			klog.Info("Current masters:")
			for _, m := range masters {
				klog.InfoS("  Master pod",
					"pod", m.PodName,
					"uid", m.PodUID,
					"namespace", m.Namespace,
					"startupTime", m.StartupTime.Format(time.RFC3339))
			}
		}
	} else {
		klog.V(2).InfoS("Reconcile", "totalPods", len(allStates), "masters", masterCount)
	}

	if o.config.ElectionMode == config.ElectionModeRaft {
		return o.handleRaftMode(ctx, localState)
	}

	if masterCount == 0 {
		return o.handleNoMaster(ctx, allStates, localState)
	}

	if masterCount == 1 {
		return o.handleSingleMaster(ctx, currentMaster, localState)
	}

	if masterCount > 1 {
		return o.handleSplitBrain(ctx, allStates, localState)
	}

	return nil
}

func (o *Orchestrator) handleNoMaster(ctx context.Context, allStates []*state.PodState, localState *state.PodState) error {
	if o.config.Standalone {
		if o.config.Debug {
			klog.Info("Standalone witness mode - not managing Redis promotion")
		}
		return nil
	}

	if o.config.Debug {
		klog.Info("========================================")
		klog.Info("NO MASTER DETECTED - Starting election")
		klog.InfoS("Election candidates", "count", len(allStates))
		for i, state := range allStates {
			klog.InfoS("Candidate",
				"rank", i+1,
				"pod", state.PodName,
				"uid", state.PodUID,
				"namespace", state.Namespace,
				"startupTime", state.StartupTime.Format(time.RFC3339),
				"ip", state.PodIP)
		}
	} else {
		klog.Info("No master found, initiating election")
	}

	// Deterministic election ordering: startup time (oldest first), then pod name,
	// then pod UID. This ensures all nodes elect the same master without coordination.
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

	if o.config.Debug {
		klog.Info("Election result:")
		klog.InfoS("ELECTED MASTER",
			"pod", elected.PodName,
			"uid", elected.PodUID,
			"namespace", elected.Namespace,
			"startupTime", elected.StartupTime.Format(time.RFC3339),
			"reason", o.getElectionReason(allStates))
	} else {
		klog.InfoS("Elected new master", "pod", elected.PodName, "startupTime", elected.StartupTime)
	}

	if elected.PodUID == o.podUID {
		if o.config.Debug {
			klog.Info("WE ARE THE ELECTED MASTER - Promoting")
		}
		if err := o.promoteToMaster(ctx); err != nil {
			return fmt.Errorf("failed to promote to master: %w", err)
		}
	} else {
		if o.config.Debug {
			klog.InfoS("We are not the elected master",
				"electedPod", elected.PodName,
				"electedUID", elected.PodUID,
				"ourPod", o.config.PodName,
				"ourUID", o.podUID)
		}
		if localState.IsMaster {
			if o.config.Debug {
				klog.Info("We are currently master but should not be - Demoting")
			}
			if err := o.demoteToReplica(ctx); err != nil {
				return fmt.Errorf("failed to demote to replica: %w", err)
			}
		} else if o.config.Debug {
			klog.Info("Already correctly configured as replica")
		}
	}

	return nil
}

func (o *Orchestrator) getElectionReason(sortedStates []*state.PodState) string {
	if len(sortedStates) < 2 {
		return "only candidate"
	}

	elected := sortedStates[0]
	runner := sortedStates[1]

	if !elected.StartupTime.Equal(runner.StartupTime) {
		return fmt.Sprintf("oldest startup time (%s vs %s)",
			elected.StartupTime.Format(time.RFC3339),
			runner.StartupTime.Format(time.RFC3339))
	}

	if elected.PodName != runner.PodName {
		return fmt.Sprintf("tie-breaker: pod name (%s < %s)", elected.PodName, runner.PodName)
	}

	return fmt.Sprintf("tie-breaker: pod UID (%s < %s) - multi-site scenario",
		elected.PodUID[:8], runner.PodUID[:8])
}

func (o *Orchestrator) handleSingleMaster(ctx context.Context, master *state.PodState, localState *state.PodState) error {
	if o.config.Standalone {
		if o.config.Debug {
			klog.InfoS("Standalone witness - master exists",
				"masterPod", master.PodName,
				"masterUID", master.PodUID)
		}
		return nil
	}

	if o.config.Debug {
		klog.InfoS("Single master detected",
			"masterPod", master.PodName,
			"masterUID", master.PodUID,
			"masterNamespace", master.Namespace,
			"weAreMaster", master.PodUID == o.podUID)
	}

	if master.PodUID == o.podUID {
		if o.config.Debug {
			klog.Info("We are the master - ensuring label is set")
		}
		return o.ensureMasterLabel(ctx)
	}

	if localState.IsMaster {
		if o.config.Debug {
			klog.InfoS("We are master but should not be - demoting",
				"correctMaster", master.PodName,
				"correctMasterUID", master.PodUID)
		} else {
			klog.InfoS("We are master but should not be", "correctMaster", master.PodName)
		}
		return o.demoteToReplica(ctx)
	}

	if o.config.Debug {
		klog.Info("Correctly configured as replica")
	}

	return nil
}

func (o *Orchestrator) handleRaftMode(ctx context.Context, localState *state.PodState) error {
	if o.config.Standalone {
		if o.config.Debug {
			klog.Info("Standalone witness mode - not managing Redis, only participating in Raft")
		}
		return nil
	}

	isRaftLeader := o.electionStrategy.IsLeader()

	if o.config.Debug {
		klog.InfoS("Raft mode reconciliation",
			"isRaftLeader", isRaftLeader,
			"localIsMaster", localState.IsMaster)
	}

	if isRaftLeader {
		if !localState.IsMaster {
			if o.config.Debug {
				klog.Info("We are the Raft leader but not Redis master - promoting")
			} else {
				klog.Info("Raft leader detected - promoting to Redis master")
			}
			return o.promoteToMaster(ctx)
		}
		if o.config.Debug {
			klog.Info("We are the Raft leader and Redis master - ensuring label")
		}
		return o.ensureMasterLabel(ctx)
	}

	if localState.IsMaster {
		leaderName, leaderUID, err := o.electionStrategy.GetLeader()
		if err != nil {
			if o.config.Debug {
				klog.InfoS("We are not the Raft leader (leader unknown) - demoting to replica")
			} else {
				klog.Info("Not Raft leader - demoting to replica")
			}
		} else {
			if o.config.Debug {
				klog.InfoS("We are not the Raft leader - demoting to replica",
					"raftLeader", leaderName,
					"raftLeaderUID", leaderUID[:8])
			} else {
				klog.InfoS("Not Raft leader - demoting to replica", "leader", leaderName)
			}
		}
		return o.demoteToReplica(ctx)
	}

	if o.config.Debug {
		klog.Info("Correctly configured as replica (not Raft leader)")
	}
	return nil
}

func (o *Orchestrator) handleSplitBrain(ctx context.Context, allStates []*state.PodState, localState *state.PodState) error {
	if o.config.Standalone {
		klog.Warning("Standalone witness detected split-brain (not managing Redis)")
		return nil
	}

	if o.config.Debug {
		klog.Warning("========================================")
		klog.Warning("SPLIT BRAIN DETECTED - Multiple masters exist!")
	} else {
		klog.Warning("Split brain detected - multiple masters exist")
	}

	var masters []*state.PodState
	for _, st := range allStates {
		if st.IsMaster {
			masters = append(masters, st)
		}
	}

	if o.config.Debug {
		klog.InfoS("Split brain masters", "count", len(masters))
		for i, m := range masters {
			klog.InfoS("Master",
				"index", i,
				"pod", m.PodName,
				"uid", m.PodUID,
				"namespace", m.Namespace,
				"startupTime", m.StartupTime.Format(time.RFC3339))
		}
	}

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

	if o.config.Debug {
		klog.InfoS("Split brain resolution decision",
			"keepPod", keepMaster.PodName,
			"keepUID", keepMaster.PodUID,
			"keepNamespace", keepMaster.Namespace,
			"reason", o.getElectionReason(masters))

		klog.Info("Masters to demote:")
		for i := 1; i < len(masters); i++ {
			klog.InfoS("  Will demote",
				"pod", masters[i].PodName,
				"uid", masters[i].PodUID,
				"namespace", masters[i].Namespace)
		}
	} else {
		klog.InfoS("Resolving split brain, keeping master", "pod", keepMaster.PodName)
	}

	// Split-brain resolution: if we're a master but not the chosen one, demote.
	// The chosen master is determined by the same deterministic ordering used in elections.
	if localState.IsMaster && localState.PodUID != keepMaster.PodUID {
		if o.config.Debug {
			klog.Warning("WE MUST DEMOTE - We are not the chosen master")
		}
		return o.demoteToReplica(ctx)
	}

	if o.config.Debug {
		if localState.IsMaster {
			klog.Info("We are the chosen master - keeping role")
		} else {
			klog.Info("We are correctly a replica")
		}
	}

	return nil
}

func (o *Orchestrator) promoteToMaster(ctx context.Context) error {
	if o.config.Debug {
		klog.Info("========================================")
		klog.InfoS("PROMOTING TO MASTER",
			"pod", o.config.PodName,
			"uid", o.podUID,
			"namespace", o.config.Namespace)
	} else {
		klog.Info("Promoting to master")
	}

	if err := o.redisClient.PromoteToMaster(ctx); err != nil {
		return err
	}

	if o.config.Debug {
		klog.Info("Redis promotion successful, setting pod label")
	}

	return o.ensureMasterLabel(ctx)
}

func (o *Orchestrator) demoteToReplica(ctx context.Context) error {
	if o.config.Debug {
		klog.Info("========================================")
		klog.InfoS("DEMOTING TO REPLICA",
			"pod", o.config.PodName,
			"uid", o.podUID,
			"namespace", o.config.Namespace,
			"masterService", o.config.RedisServiceName)
	} else {
		klog.Info("Demoting to replica")
	}

	if err := o.removeMasterLabel(ctx); err != nil {
		klog.ErrorS(err, "Failed to remove master label")
	}

	if o.config.Debug {
		klog.InfoS("Removed master label, configuring replication",
			"masterService", o.config.RedisServiceName,
			"port", o.config.RedisPort)
	}

	return o.redisClient.SetReplicaOf(ctx, o.config.RedisServiceName, o.config.RedisPort)
}

func (o *Orchestrator) ensureMasterLabel(ctx context.Context) error {
	pod, err := o.kubeClient.CoreV1().Pods(o.config.Namespace).Get(
		ctx,
		o.config.PodName,
		metav1.GetOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to get pod: %w", err)
	}

	// Idempotent label update: only update if label is missing or incorrect.
	// This avoids unnecessary API calls and potential conflicts.
	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}

	if pod.Labels[MasterLabel] == MasterLabelValue {
		return nil
	}

	pod.Labels[MasterLabel] = MasterLabelValue

	_, err = o.kubeClient.CoreV1().Pods(o.config.Namespace).Update(ctx, pod, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update pod label: %w", err)
	}

	klog.Info("Set master label on pod")
	return nil
}

func (o *Orchestrator) removeMasterLabel(ctx context.Context) error {
	pod, err := o.kubeClient.CoreV1().Pods(o.config.Namespace).Get(
		ctx,
		o.config.PodName,
		metav1.GetOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to get pod: %w", err)
	}

	if pod.Labels == nil || pod.Labels[MasterLabel] == "" {
		return nil
	}

	delete(pod.Labels, MasterLabel)

	_, err = o.kubeClient.CoreV1().Pods(o.config.Namespace).Update(ctx, pod, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update pod label: %w", err)
	}

	klog.Info("Removed master label from pod")
	return nil
}

func (o *Orchestrator) setupHTTPServer() {
	mux := http.NewServeMux()

	mux.HandleFunc("/state", o.authenticator.Middleware(o.handleStateRequest))
	mux.HandleFunc("/health", o.handleHealthRequest)

	// Raft-specific endpoints: only register if using Raft mode and the strategy
	// implements the required handlers. This allows dynamic cluster management.
	if o.config.ElectionMode == config.ElectionModeRaft {
		if raftStrategy, ok := o.electionStrategy.(interface {
			HandleRaftStatus(http.ResponseWriter, *http.Request)
			HandleAddVoter(http.ResponseWriter, *http.Request)
			HandleAddNonvoter(http.ResponseWriter, *http.Request)
			HandleRaftPeers(http.ResponseWriter, *http.Request)
		}); ok {
			mux.HandleFunc("/raft/status", o.authenticator.Middleware(raftStrategy.HandleRaftStatus))
			mux.HandleFunc("/raft/add-voter", o.authenticator.Middleware(raftStrategy.HandleAddVoter))
			mux.HandleFunc("/raft/add-nonvoter", o.authenticator.Middleware(raftStrategy.HandleAddNonvoter))
			mux.HandleFunc("/raft/peers", o.authenticator.Middleware(raftStrategy.HandleRaftPeers))

			if o.config.Debug {
				klog.Info("Registered Raft HTTP endpoints: /raft/status, /raft/add-voter, /raft/add-nonvoter, /raft/peers")
			}
		}
	}

	o.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", HTTPPort),
		Handler: mux,
	}
}

func (o *Orchestrator) handleStateRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	st, err := o.getLocalState(ctx)
	if err != nil {
		klog.ErrorS(err, "Failed to get local state for HTTP request")
		http.Error(w, "Failed to get state", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(st)
}

func (o *Orchestrator) handleHealthRequest(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// shutdown gracefully stops all components: election strategy, HTTP server, and Redis client.
// Uses timeouts to prevent hanging during shutdown.
func (o *Orchestrator) shutdown() error {
	klog.Info("Shutting down orchestrator")

	// Stop election strategy first to prevent new leadership changes during shutdown.
	if err := o.electionStrategy.Stop(); err != nil {
		klog.ErrorS(err, "Failed to stop election strategy")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Graceful HTTP shutdown: stops accepting new connections and waits for existing ones.
	if err := o.httpServer.Shutdown(ctx); err != nil {
		klog.ErrorS(err, "Failed to shutdown HTTP server")
	}

	if o.redisClient != nil {
		if err := o.redisClient.Close(); err != nil {
			klog.ErrorS(err, "Failed to close Redis client")
		}
	}

	return nil
}

// discoverLoadBalancerIP discovers a LoadBalancer service that has a pod-name label selector
// matching the current pod and returns its external IP. Returns the IP, service name, and error.
func discoverLoadBalancerIP(kubeClient kubernetes.Interface, namespace, podName string) (string, string, error) {
	// Get the current pod to check its labels
	pod, err := kubeClient.CoreV1().Pods(namespace).Get(
		context.Background(),
		podName,
		metav1.GetOptions{},
	)
	if err != nil {
		return "", "", fmt.Errorf("failed to get pod %s: %w", podName, err)
	}

	// List all services in the namespace
	services, err := kubeClient.CoreV1().Services(namespace).List(
		context.Background(),
		metav1.ListOptions{},
	)
	if err != nil {
		return "", "", fmt.Errorf("failed to list services: %w", err)
	}

	// Find LoadBalancer services that match this pod via pod-name selector
	for _, svc := range services.Items {
		// Only consider LoadBalancer services
		if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
			continue
		}

		// Check if the service selector matches the pod, with special check for pod name
		if !matchesSelectorWithPodName(pod.Labels, svc.Spec.Selector, podName) {
			continue
		}

		// Found a matching service - get the external IP
		if len(svc.Status.LoadBalancer.Ingress) == 0 {
			// No external IP assigned yet - return empty string (caller will use pod IP)
			klog.InfoS("LoadBalancer service found but no external IP yet",
				"service", svc.Name,
				"pod", podName)
			return "", svc.Name, nil
		}

		ingress := svc.Status.LoadBalancer.Ingress[0]
		if ingress.IP != "" {
			return ingress.IP, svc.Name, nil
		}
		if ingress.Hostname != "" {
			// Some cloud providers use hostname instead of IP
			// For now, return empty and let caller use pod IP
			// In the future, we could resolve the hostname to IP
			klog.InfoS("LoadBalancer has hostname instead of IP",
				"service", svc.Name,
				"hostname", ingress.Hostname)
			return "", svc.Name, nil
		}
	}

	// No matching LoadBalancer service found
	klog.InfoS("No LoadBalancer service found with pod-name selector",
		"pod", podName,
		"namespace", namespace)
	return "", "", nil
}

// matchesSelectorWithPodName checks if the pod labels match the service selector,
// with a special requirement that one of the selectors must specifically match the pod name/index.
// This ensures the LoadBalancer service is bound to this specific pod, not just any pod matching other labels.
func matchesSelectorWithPodName(podLabels map[string]string, selector map[string]string, podName string) bool {
	if len(selector) == 0 {
		return false
	}

	// Check for pod name/index matching in common selector keys
	podNameMatched := false
	podNameSelectorKeys := []string{
		"statefulset.kubernetes.io/pod-name", // Kubernetes StatefulSet pod name label
		"pod-name",                           // Pod name label selector
		"pod",                                // Custom pod label
	}

	// First, verify all selectors match
	for key, value := range selector {
		if podLabels[key] != value {
			return false
		}
		// Check if this selector key is one that should match the pod name
		for _, podKey := range podNameSelectorKeys {
			if key == podKey && value == podName {
				podNameMatched = true
				break
			}
		}
	}

	// Require that at least one selector specifically matches the pod name/index
	if !podNameMatched {
		// Check if any selector value matches the pod name (fallback check)
		for _, value := range selector {
			if value == podName {
				podNameMatched = true
				break
			}
		}
	}

	if !podNameMatched {
		klog.InfoS("Service selector does not include pod name/index match",
			"podName", podName,
			"selector", selector)
		return false
	}

	return true
}
