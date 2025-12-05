package redis

import (
	"context"
	"crypto/tls"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-redis/redis/v8"
	"k8s.io/klog/v2"
)

type Client struct {
	client *redis.Client
	useTLS bool
}

type ReplicationInfo struct {
	Role             string
	MasterHost       string
	MasterPort       int
	ConnectedSlaves  int
	MasterLinkStatus string
}

func NewClient(host string, port int, password string, useTLS bool, skipVerify bool) (*Client, error) {
	opts := &redis.Options{
		Addr:     fmt.Sprintf("%s:%d", host, port),
		Password: password,
		DB:       0,
	}

	// TLS configuration: enforce minimum TLS 1.2 for security.
	// SkipVerify should only be used in development/testing environments.
	if useTLS {
		opts.TLSConfig = &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: skipVerify,
		}
		if skipVerify {
			klog.Warning("TLS certificate verification is disabled - not recommended for production")
		}
	}

	client := redis.NewClient(opts)

	// Verify connection immediately to fail fast if Redis is unreachable.
	// This prevents creating a client that appears valid but can't actually connect.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	klog.InfoS("Connected to Redis", "host", host, "port", port, "tls", useTLS)

	return &Client{
		client: client,
		useTLS: useTLS,
	}, nil
}

func (c *Client) GetReplicationInfo(ctx context.Context) (*ReplicationInfo, error) {
	info, err := c.client.Info(ctx, "replication").Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get replication info: %w", err)
	}

	return parseReplicationInfo(info)
}

// PromoteToMaster removes replication configuration, making this Redis instance
// a standalone master. This is idempotent if already master.
func (c *Client) PromoteToMaster(ctx context.Context) error {
	klog.Info("Promoting local Redis to master")
	
	result := c.client.Do(ctx, "REPLICAOF", "NO", "ONE")
	if err := result.Err(); err != nil {
		return fmt.Errorf("failed to promote to master: %w", err)
	}

	klog.Info("Successfully promoted to master")
	return nil
}

// SetReplicaOf configures this Redis instance to replicate from the specified master.
// If already replicating from this master, the command is effectively a no-op.
func (c *Client) SetReplicaOf(ctx context.Context, masterHost string, masterPort int) error {
	klog.InfoS("Configuring as replica", "masterHost", masterHost, "masterPort", masterPort)
	
	result := c.client.Do(ctx, "REPLICAOF", masterHost, strconv.Itoa(masterPort))
	if err := result.Err(); err != nil {
		return fmt.Errorf("failed to set replica: %w", err)
	}

	klog.Info("Successfully configured as replica")
	return nil
}

func (c *Client) IsHealthy(ctx context.Context) bool {
	err := c.client.Ping(ctx).Err()
	return err == nil
}

func (c *Client) Close() error {
	return c.client.Close()
}

// parseReplicationInfo parses Redis INFO replication output.
// Redis INFO format uses \r\n line endings and # for comments.
func parseReplicationInfo(info string) (*ReplicationInfo, error) {
	lines := strings.Split(info, "\r\n")
	result := &ReplicationInfo{}

	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "role":
			result.Role = value
		case "master_host":
			result.MasterHost = value
		case "master_port":
			if port, err := strconv.Atoi(value); err == nil {
				result.MasterPort = port
			}
		case "connected_slaves":
			if count, err := strconv.Atoi(value); err == nil {
				result.ConnectedSlaves = count
			}
		case "master_link_status":
			result.MasterLinkStatus = value
		}
	}

	// Role is required - if missing, the INFO output may be malformed or incomplete.
	if result.Role == "" {
		return nil, fmt.Errorf("could not parse role from replication info")
	}

	return result, nil
}

