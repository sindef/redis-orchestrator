package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/sindef/redis-orchestrator/pkg/config"
	redisclient "github.com/sindef/redis-orchestrator/pkg/redis"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	MasterLabel      = "redis-role"
	MasterLabelValue = "master"
	HTTPPort         = 8080
)

// PodState represents the state of a Redis pod
type PodState struct {
	PodName     string    `json:"pod_name"`
	PodIP       string    `json:"pod_ip"`
	PodUID      string    `json:"pod_uid"`      // Added for multi-site uniqueness
	Namespace   string    `json:"namespace"`    // Added for multi-site context
	IsMaster    bool      `json:"is_master"`
	IsHealthy   bool      `json:"is_healthy"`
	StartupTime time.Time `json:"startup_time"`
	LastSeen    time.Time `json:"last_seen"`
}

// Orchestrator manages Redis master election and failover
type Orchestrator struct {
	config      *config.Config
	kubeClient  kubernetes.Interface
	redisClient *redisclient.Client

	startupTime time.Time
	podIP       string
	podUID      string

	// State tracking
	mu         sync.RWMutex
	peerStates map[string]*PodState // key is pod name

	// HTTP server for peer communication
	httpServer *http.Server
}

// New creates a new orchestrator
func New(cfg *config.Config, kubeClient kubernetes.Interface) (*Orchestrator, error) {
	// Create Redis client
	redisClient, err := redisclient.NewClient(
		cfg.RedisHost,
		cfg.RedisPort,
		cfg.RedisPassword,
		cfg.RedisTLS,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis client: %w", err)
	}

	// Get pod IP
	pod, err := kubeClient.CoreV1().Pods(cfg.Namespace).Get(
		context.Background(),
		cfg.PodName,
		metav1.GetOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get pod: %w", err)
	}

	o := &Orchestrator{
		config:      cfg,
		kubeClient:  kubeClient,
		redisClient: redisClient,
		startupTime: time.Now(),
		podIP:       pod.Status.PodIP,
		podUID:      string(pod.UID),
		peerStates:  make(map[string]*PodState),
	}

	// Setup HTTP server for peer communication
	o.setupHTTPServer()

	return o, nil
}

// Run starts the orchestrator main loop
func (o *Orchestrator) Run(ctx context.Context) error {
	klog.InfoS("Starting orchestrator", "pod", o.config.PodName, "startupTime", o.startupTime)

	// Start HTTP server
	go func() {
		klog.InfoS("Starting HTTP server", "port", HTTPPort)
		if err := o.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			klog.ErrorS(err, "HTTP server error")
		}
	}()

	// Main sync loop
	ticker := time.NewTicker(o.config.SyncInterval)
	defer ticker.Stop()

	// Initial sync
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

// syncState performs a full state sync cycle
func (o *Orchestrator) syncState(ctx context.Context) error {
	if o.config.Debug {
		klog.Info("============================================")
		klog.InfoS("Starting state sync cycle", "pod", o.config.PodName, "namespace", o.config.Namespace)
	} else {
		klog.V(2).Info("Starting state sync")
	}

	// 1. Check local Redis state
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

	// 2. Discover and query peers
	peerStates, err := o.discoverAndQueryPeers(ctx)
	if err != nil {
		klog.ErrorS(err, "Failed to discover peers")
		// Continue with available information
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

	// 3. Update peer states
	o.updatePeerStates(localState, peerStates)

	// 4. Determine desired state and act
	return o.reconcile(ctx, localState)
}

// getLocalState checks the local Redis instance
func (o *Orchestrator) getLocalState(ctx context.Context) (*PodState, error) {
	info, err := o.redisClient.GetReplicationInfo(ctx)
	if err != nil {
		return nil, err
	}

	state := &PodState{
		PodName:     o.config.PodName,
		PodIP:       o.podIP,
		PodUID:      o.podUID,
		Namespace:   o.config.Namespace,
		IsMaster:    info.Role == "master",
		IsHealthy:   o.redisClient.IsHealthy(ctx),
		StartupTime: o.startupTime,
		LastSeen:    time.Now(),
	}

	klog.V(2).InfoS("Local state", "isMaster", state.IsMaster, "isHealthy", state.IsHealthy)

	return state, nil
}

// discoverAndQueryPeers finds other orchestrator instances
func (o *Orchestrator) discoverAndQueryPeers(ctx context.Context) (map[string]*PodState, error) {
	pods, err := o.kubeClient.CoreV1().Pods(o.config.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: o.config.LabelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	states := make(map[string]*PodState)

	for _, pod := range pods.Items {
		// Skip self
		if pod.Name == o.config.PodName {
			continue
		}

		// Skip pods that aren't ready
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}

		// Query peer
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

// queryPeer queries another orchestrator instance
func (o *Orchestrator) queryPeer(ctx context.Context, peerIP string) (*PodState, error) {
	url := fmt.Sprintf("http://%s:%d/state", peerIP, HTTPPort)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
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

	var state PodState
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return nil, err
	}

	return &state, nil
}

// updatePeerStates updates the internal peer state cache
func (o *Orchestrator) updatePeerStates(localState *PodState, peerStates map[string]*PodState) {
	o.mu.Lock()
	defer o.mu.Unlock()

	// Add/update local state
	o.peerStates[o.config.PodName] = localState

	// Add/update peer states
	for podName, state := range peerStates {
		o.peerStates[podName] = state
	}

	// Remove stale peers (not seen in last 60 seconds)
	staleThreshold := time.Now().Add(-60 * time.Second)
	for podName, state := range o.peerStates {
		if podName != o.config.PodName && state.LastSeen.Before(staleThreshold) {
			klog.InfoS("Removing stale peer", "pod", podName)
			delete(o.peerStates, podName)
		}
	}
}

// reconcile determines if action is needed and performs it
func (o *Orchestrator) reconcile(ctx context.Context, localState *PodState) error {
	o.mu.RLock()
	allStates := make([]*PodState, 0, len(o.peerStates))
	for _, state := range o.peerStates {
		if state.IsHealthy {
			allStates = append(allStates, state)
		}
	}
	o.mu.RUnlock()

	if len(allStates) == 0 {
		klog.Warning("No healthy pods found including self")
		return nil
	}

	// Count current masters
	masterCount := 0
	var currentMaster *PodState
	var masters []*PodState
	for _, state := range allStates {
		if state.IsMaster {
			masterCount++
			currentMaster = state
			masters = append(masters, state)
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

	// Case 1: No master exists - elect one
	if masterCount == 0 {
		return o.handleNoMaster(ctx, allStates, localState)
	}

	// Case 2: Exactly one master - ensure we're correctly configured
	if masterCount == 1 {
		return o.handleSingleMaster(ctx, currentMaster, localState)
	}

	// Case 3: Multiple masters (split brain) - resolve
	if masterCount > 1 {
		return o.handleSplitBrain(ctx, allStates, localState)
	}

	return nil
}

// handleNoMaster elects a new master when none exists
func (o *Orchestrator) handleNoMaster(ctx context.Context, allStates []*PodState, localState *PodState) error {
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

	// Sort by:
	// 1. Startup time (oldest first)
	// 2. Pod name (lexicographically) - handles single-site
	// 3. UID (lexicographically) - handles multi-site with same pod names
	sort.Slice(allStates, func(i, j int) bool {
		if allStates[i].StartupTime.Equal(allStates[j].StartupTime) {
			if allStates[i].PodName == allStates[j].PodName {
				// Same pod name (multi-site scenario) - use UID
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

	// If we are elected, promote ourselves
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
		// We are not master, ensure we're a replica
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

// getElectionReason explains why the elected master was chosen
func (o *Orchestrator) getElectionReason(sortedStates []*PodState) string {
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

// handleSingleMaster ensures correct configuration with one master
func (o *Orchestrator) handleSingleMaster(ctx context.Context, master *PodState, localState *PodState) error {
	// If we are the master, ensure pod label is set
	if master.PodName == o.config.PodName {
		return o.ensureMasterLabel(ctx)
	}

	// We are not the master - ensure we're configured as replica
	if localState.IsMaster {
		klog.InfoS("We are master but should not be", "correctMaster", master.PodName)
		return o.demoteToReplica(ctx)
	}

	return nil
}

// handleSplitBrain resolves multiple masters
func (o *Orchestrator) handleSplitBrain(ctx context.Context, allStates []*PodState, localState *PodState) error {
	klog.Warning("Split brain detected - multiple masters exist")

	// Keep the oldest master, demote others
	var masters []*PodState
	for _, state := range allStates {
		if state.IsMaster {
			masters = append(masters, state)
		}
	}

	sort.Slice(masters, func(i, j int) bool {
		if masters[i].StartupTime.Equal(masters[j].StartupTime) {
			return masters[i].PodName < masters[j].PodName
		}
		return masters[i].StartupTime.Before(masters[j].StartupTime)
	})

	keepMaster := masters[0]
	klog.InfoS("Resolving split brain, keeping master", "pod", keepMaster.PodName)

	// If we are not the chosen master but currently master, demote
	if localState.IsMaster && localState.PodName != keepMaster.PodName {
		return o.demoteToReplica(ctx)
	}

	return nil
}

// promoteToMaster promotes this instance to master
func (o *Orchestrator) promoteToMaster(ctx context.Context) error {
	klog.Info("Promoting to master")

	// First, set Redis to master
	if err := o.redisClient.PromoteToMaster(ctx); err != nil {
		return err
	}

	// Then, set pod label
	return o.ensureMasterLabel(ctx)
}

// demoteToReplica demotes this instance to replica
func (o *Orchestrator) demoteToReplica(ctx context.Context) error {
	klog.Info("Demoting to replica")

	// Remove master label
	if err := o.removeMasterLabel(ctx); err != nil {
		klog.ErrorS(err, "Failed to remove master label")
	}

	// Set as replica of service
	return o.redisClient.SetReplicaOf(ctx, o.config.RedisServiceName, o.config.RedisPort)
}

// ensureMasterLabel ensures the master label is set on our pod
func (o *Orchestrator) ensureMasterLabel(ctx context.Context) error {
	pod, err := o.kubeClient.CoreV1().Pods(o.config.Namespace).Get(
		ctx,
		o.config.PodName,
		metav1.GetOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to get pod: %w", err)
	}

	if pod.Labels == nil {
		pod.Labels = make(map[string]string)
	}

	if pod.Labels[MasterLabel] == MasterLabelValue {
		return nil // Already set
	}

	pod.Labels[MasterLabel] = MasterLabelValue

	_, err = o.kubeClient.CoreV1().Pods(o.config.Namespace).Update(ctx, pod, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update pod label: %w", err)
	}

	klog.Info("Set master label on pod")
	return nil
}

// removeMasterLabel removes the master label from our pod
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
		return nil // Already removed
	}

	delete(pod.Labels, MasterLabel)

	_, err = o.kubeClient.CoreV1().Pods(o.config.Namespace).Update(ctx, pod, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update pod label: %w", err)
	}

	klog.Info("Removed master label from pod")
	return nil
}

// setupHTTPServer configures the HTTP server for peer communication
func (o *Orchestrator) setupHTTPServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/state", o.handleStateRequest)
	mux.HandleFunc("/health", o.handleHealthRequest)

	o.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", HTTPPort),
		Handler: mux,
	}
}

// handleStateRequest returns the current state
func (o *Orchestrator) handleStateRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	state, err := o.getLocalState(ctx)
	if err != nil {
		klog.ErrorS(err, "Failed to get local state for HTTP request")
		http.Error(w, "Failed to get state", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(state)
}

// handleHealthRequest returns health status
func (o *Orchestrator) handleHealthRequest(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// shutdown gracefully shuts down the orchestrator
func (o *Orchestrator) shutdown() error {
	klog.Info("Shutting down orchestrator")

	// Shutdown HTTP server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := o.httpServer.Shutdown(ctx); err != nil {
		klog.ErrorS(err, "Failed to shutdown HTTP server")
	}

	// Close Redis client
	if err := o.redisClient.Close(); err != nil {
		klog.ErrorS(err, "Failed to close Redis client")
	}

	return nil
}
