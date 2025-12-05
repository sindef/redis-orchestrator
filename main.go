package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
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
	// Parse flags
	cfg := &config.Config{}
	flag.StringVar(&cfg.RedisHost, "redis-host", "localhost", "Redis host")
	flag.IntVar(&cfg.RedisPort, "redis-port", 6379, "Redis port")
	flag.StringVar(&cfg.RedisPassword, "redis-password", "", "Redis password (or use REDIS_PASSWORD env)")
	flag.BoolVar(&cfg.RedisTLS, "redis-tls", false, "Use TLS for Redis connection")
	flag.StringVar(&cfg.RedisServiceName, "redis-service", "redis", "Redis service name for replica configuration")
	flag.DurationVar(&cfg.SyncInterval, "sync-interval", 15*time.Second, "Interval between state syncs")
	flag.StringVar(&cfg.PodName, "pod-name", os.Getenv("POD_NAME"), "Pod name (from downward API)")
	flag.StringVar(&cfg.Namespace, "namespace", os.Getenv("POD_NAMESPACE"), "Namespace (from downward API)")
	flag.StringVar(&cfg.LabelSelector, "label-selector", "app=redis", "Label selector to find Redis pods")
	flag.BoolVar(&cfg.Debug, "debug", false, "Enable debug logging (use --debug=true)")
	flag.Parse()

	// Override password from env if set
	if envPass := os.Getenv("REDIS_PASSWORD"); envPass != "" && cfg.RedisPassword == "" {
		cfg.RedisPassword = envPass
	}

	klog.InfoS("Starting Redis Orchestrator", "version", version, "pod", cfg.PodName, "namespace", cfg.Namespace)

	// Validate required config
	if cfg.PodName == "" {
		klog.Fatal("POD_NAME is required")
	}
	if cfg.Namespace == "" {
		klog.Fatal("POD_NAMESPACE is required")
	}

	// Create Kubernetes client
	kubeConfig, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatalf("Failed to create in-cluster config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		klog.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Create orchestrator
	orch, err := orchestrator.New(cfg, clientset)
	if err != nil {
		klog.Fatalf("Failed to create orchestrator: %v", err)
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		klog.InfoS("Received signal, shutting down", "signal", sig)
		cancel()
	}()

	// Run orchestrator
	if err := orch.Run(ctx); err != nil {
		klog.Fatalf("Orchestrator error: %v", err)
	}

	klog.Info("Shutdown complete")
}
