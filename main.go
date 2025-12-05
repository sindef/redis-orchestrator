package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sindef/redis-orchestrator/pkg/config"
	"github.com/sindef/redis-orchestrator/pkg/orchestrator"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

var (
	version = "dev"
)

func main() {
	cfg := &config.Config{}
	var electionMode string
	var raftPeersStr string
	var raftBootstrapSet bool
	
	flag.StringVar(&cfg.RedisHost, "redis-host", "localhost", "Redis host")
	flag.IntVar(&cfg.RedisPort, "redis-port", 6379, "Redis port")
	flag.StringVar(&cfg.RedisPassword, "redis-password", "", "Redis password (or use REDIS_PASSWORD env)")
	flag.BoolVar(&cfg.RedisTLS, "redis-tls", false, "Use TLS for Redis connection")
	flag.BoolVar(&cfg.RedisTLSSkipVerify, "redis-tls-skip-verify", false, "Skip TLS certificate verification (use --redis-tls-skip-verify=true)")
	flag.StringVar(&cfg.RedisServiceName, "redis-service", "redis", "Redis service name for replica configuration")
	flag.DurationVar(&cfg.SyncInterval, "sync-interval", 15*time.Second, "Interval between state syncs")
	
	flag.StringVar(&cfg.PodName, "pod-name", os.Getenv("POD_NAME"), "Pod name (from downward API)")
	flag.StringVar(&cfg.Namespace, "namespace", os.Getenv("POD_NAMESPACE"), "Namespace (from downward API)")
	flag.StringVar(&cfg.LabelSelector, "label-selector", "app=redis", "Label selector to find Redis pods")
	
	flag.StringVar(&electionMode, "election-mode", "deterministic", "Election mode: deterministic or raft")
	flag.StringVar(&cfg.RaftBindAddr, "raft-bind", "0.0.0.0:7000", "Raft bind address (e.g., 0.0.0.0:7000)")
	flag.StringVar(&raftPeersStr, "raft-peers", "", "Comma-separated list of Raft peer addresses (e.g., pod-0:7000,pod-1:7000)")
	flag.StringVar(&cfg.RaftDataDir, "raft-data-dir", "/var/lib/redis-orchestrator/raft", "Directory for Raft data storage")
	flag.BoolVar(&raftBootstrapSet, "raft-bootstrap", false, "Bootstrap Raft cluster (auto-detected if not set)")
	
	flag.StringVar(&cfg.SharedSecret, "shared-secret", os.Getenv("SHARED_SECRET"), "Shared secret for peer authentication")
	
	flag.BoolVar(&cfg.Standalone, "standalone", false, "Run as Raft witness without managing Redis (use --standalone=true)")
	
	flag.BoolVar(&cfg.Debug, "debug", false, "Enable debug logging (use --debug=true)")
	flag.Parse()

	switch electionMode {
	case "deterministic", "":
		cfg.ElectionMode = config.ElectionModeDeterministic
	case "raft":
		cfg.ElectionMode = config.ElectionModeRaft
	default:
		klog.Fatalf("Invalid election mode: %s (must be 'deterministic' or 'raft')", electionMode)
	}

	if raftPeersStr != "" {
		cfg.RaftPeers = strings.Split(raftPeersStr, ",")
	}

	if envPass := os.Getenv("REDIS_PASSWORD"); envPass != "" && cfg.RedisPassword == "" {
		cfg.RedisPassword = envPass
	}
	
	if envSecret := os.Getenv("SHARED_SECRET"); envSecret != "" && cfg.SharedSecret == "" {
		cfg.SharedSecret = envSecret
	}

	klog.InfoS("Starting Redis Orchestrator", 
		"version", version, 
		"pod", cfg.PodName, 
		"namespace", cfg.Namespace,
		"electionMode", cfg.ElectionMode)

	if cfg.PodName == "" {
		klog.Fatal("POD_NAME is required")
	}
	if cfg.Namespace == "" {
		klog.Fatal("POD_NAMESPACE is required")
	}
	
	if cfg.ElectionMode == config.ElectionModeRaft {
		if cfg.RaftBindAddr == "" {
			klog.Fatal("--raft-bind is required for Raft mode")
		}
		if cfg.SharedSecret == "" {
			klog.Warning("No shared secret configured - peer authentication disabled (not recommended for production)")
		}
		
	// Auto-detect bootstrap: pod-0 automatically bootstraps if not explicitly set.
	// This simplifies initial cluster setup while allowing manual override.
	if strings.HasSuffix(cfg.PodName, "-0") {
		if !raftBootstrapSet {
			cfg.RaftBootstrap = true
		} else {
			cfg.RaftBootstrap = raftBootstrapSet
		}
		if cfg.RaftBootstrap {
			klog.InfoS("Bootstrap enabled", "pod", cfg.PodName, "auto", !raftBootstrapSet)
		}
	} else {
		cfg.RaftBootstrap = false
		klog.InfoS("Bootstrap disabled", "pod", cfg.PodName, "reason", "Not pod-0, will auto-join cluster")
	}
	}
	
	// Standalone mode allows running a Raft witness node without managing Redis.
	// This is useful for maintaining quorum in small clusters without additional Redis instances.
	if cfg.Standalone {
		if cfg.ElectionMode != config.ElectionModeRaft {
			klog.Fatal("--standalone mode requires --election-mode=raft")
		}
		klog.InfoS("Running in standalone witness mode", "pod", cfg.PodName)
	}

	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Failed to create in-cluster config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		klog.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	orch, err := orchestrator.New(cfg, clientset)
	if err != nil {
		klog.Fatalf("Failed to create orchestrator: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown: capture termination signals and cancel context
	// to allow orchestrator to clean up resources (Raft, HTTP server, Redis connections).
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		klog.InfoS("Received signal, shutting down", "signal", sig)
		cancel()
	}()

	if err := orch.Run(ctx); err != nil {
		klog.Fatalf("Orchestrator error: %v", err)
	}

	klog.Info("Shutdown complete")
}
