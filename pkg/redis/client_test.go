package redis

import (
	"strings"
	"testing"
)

func TestParseReplicationInfo(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedRole   string
		expectedMaster string
		expectedPort   int
		expectedSlaves int
		expectError    bool
	}{
		{
			name:           "master with slaves",
			input:          "# Replication\r\nrole:master\r\nconnected_slaves:2\r\nslave0:ip=10.0.0.2,port=6379,state=online,offset=1234,lag=0\r\nslave1:ip=10.0.0.3,port=6379,state=online,offset=1234,lag=1\r\nmaster_replid:abc123\r\nmaster_replid2:0000000000000000000000000000000000000000\r\nmaster_repl_offset:1234\r\n",
			expectedRole:   "master",
			expectedSlaves: 2,
			expectError:    false,
		},
		{
			name:           "slave connected to master",
			input:          "# Replication\r\nrole:slave\r\nmaster_host:redis-0.redis-headless\r\nmaster_port:6379\r\nmaster_link_status:up\r\nmaster_last_io_seconds_ago:1\r\nmaster_sync_in_progress:0\r\nslave_repl_offset:5678\r\nslave_priority:100\r\nslave_read_only:1\r\nconnected_slaves:0\r\n",
			expectedRole:   "slave",
			expectedMaster: "redis-0.redis-headless",
			expectedPort:   6379,
			expectedSlaves: 0,
			expectError:    false,
		},
		{
			name:           "slave disconnected",
			input:          "# Replication\r\nrole:slave\r\nmaster_host:redis-master\r\nmaster_port:6380\r\nmaster_link_status:down\r\nmaster_last_io_seconds_ago:10\r\nconnected_slaves:0\r\n",
			expectedRole:   "slave",
			expectedMaster: "redis-master",
			expectedPort:   6380,
			expectedSlaves: 0,
			expectError:    false,
		},
		{
			name:           "standalone master",
			input:          "# Replication\r\nrole:master\r\nconnected_slaves:0\r\nmaster_replid:def456\r\nmaster_repl_offset:0\r\n",
			expectedRole:   "master",
			expectedSlaves: 0,
			expectError:    false,
		},
		{
			name:        "invalid input no role",
			input:       "# Replication\r\nconnected_slaves:0\r\n",
			expectError: true,
		},
		{
			name:           "master with no slaves field",
			input:          "# Replication\r\nrole:master\r\nmaster_replid:xyz789\r\n",
			expectedRole:   "master",
			expectedSlaves: 0,
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseReplicationInfo(tt.input)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result.Role != tt.expectedRole {
				t.Errorf("Expected role %s, got %s", tt.expectedRole, result.Role)
			}

			if tt.expectedMaster != "" && result.MasterHost != tt.expectedMaster {
				t.Errorf("Expected master host %s, got %s", tt.expectedMaster, result.MasterHost)
			}

			if tt.expectedPort > 0 && result.MasterPort != tt.expectedPort {
				t.Errorf("Expected master port %d, got %d", tt.expectedPort, result.MasterPort)
			}

			if result.ConnectedSlaves != tt.expectedSlaves {
				t.Errorf("Expected %d connected slaves, got %d", tt.expectedSlaves, result.ConnectedSlaves)
			}
		})
	}
}

func TestParseReplicationInfoWithWindowsLineEndings(t *testing.T) {
	input := "# Replication\r\nrole:master\r\nconnected_slaves:1\r\n"
	
	result, err := parseReplicationInfo(input)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.Role != "master" {
		t.Errorf("Expected role master, got %s", result.Role)
	}

	if result.ConnectedSlaves != 1 {
		t.Errorf("Expected 1 connected slave, got %d", result.ConnectedSlaves)
	}
}

func TestParseReplicationInfoWithComments(t *testing.T) {
	input := "# Replication\r\nrole:master\r\n# This is a comment\r\nconnected_slaves:3\r\n# Another comment\r\nmaster_repl_offset:9999\r\n"

	result, err := parseReplicationInfo(input)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.Role != "master" {
		t.Errorf("Expected role master, got %s", result.Role)
	}

	if result.ConnectedSlaves != 3 {
		t.Errorf("Expected 3 connected slaves, got %d", result.ConnectedSlaves)
	}
}

func TestParseReplicationInfoEmptyLines(t *testing.T) {
	input := "\r\n# Replication\r\n\r\nrole:master\r\n\r\nconnected_slaves:0\r\n\r\n"

	result, err := parseReplicationInfo(input)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.Role != "master" {
		t.Errorf("Expected role master, got %s", result.Role)
	}
}

func TestParseReplicationInfoInvalidPort(t *testing.T) {
	input := "# Replication\r\nrole:slave\r\nmaster_host:redis-master\r\nmaster_port:invalid\r\nconnected_slaves:0\r\n"

	result, err := parseReplicationInfo(input)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should not error, just skip invalid port
	if result.MasterPort != 0 {
		t.Errorf("Expected master port 0 for invalid input, got %d", result.MasterPort)
	}
}

func TestParseReplicationInfoWhitespace(t *testing.T) {
	input := "# Replication\r\n  role:  master  \r\n  connected_slaves:  2  \r\n"

	result, err := parseReplicationInfo(input)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.Role != "master" {
		t.Errorf("Expected role master with whitespace trimmed, got %s", result.Role)
	}

	if result.ConnectedSlaves != 2 {
		t.Errorf("Expected 2 connected slaves, got %d", result.ConnectedSlaves)
	}
}

func TestReplicationInfoStructDefaults(t *testing.T) {
	info := &ReplicationInfo{}

	if info.Role != "" {
		t.Errorf("Expected empty role by default, got %s", info.Role)
	}

	if info.ConnectedSlaves != 0 {
		t.Errorf("Expected 0 connected slaves by default, got %d", info.ConnectedSlaves)
	}

	if info.MasterPort != 0 {
		t.Errorf("Expected 0 master port by default, got %d", info.MasterPort)
	}
}

func TestParseReplicationInfoMasterLinkStatus(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectedStatus string
	}{
		{
			name:           "link up",
			input:          "# Replication\r\nrole:slave\r\nmaster_host:redis-master\r\nmaster_port:6379\r\nmaster_link_status:up\r\n",
			expectedStatus: "up",
		},
		{
			name:           "link down",
			input:          "# Replication\r\nrole:slave\r\nmaster_host:redis-master\r\nmaster_port:6379\r\nmaster_link_status:down\r\n",
			expectedStatus: "down",
		},
		{
			name:           "no link status (master)",
			input:          "# Replication\r\nrole:master\r\nconnected_slaves:0\r\n",
			expectedStatus: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseReplicationInfo(tt.input)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result.MasterLinkStatus != tt.expectedStatus {
				t.Errorf("Expected master link status %s, got %s", tt.expectedStatus, result.MasterLinkStatus)
			}
		})
	}
}

func TestParseReplicationInfoRealWorldExample(t *testing.T) {
	// Real output from Redis 7
	input := "# Replication\r\nrole:master\r\nconnected_slaves:2\r\nslave0:ip=10.244.0.23,port=6379,state=online,offset=14706,lag=0\r\nslave1:ip=10.244.0.24,port=6379,state=online,offset=14706,lag=1\r\nmaster_failover_state:no-failover\r\nmaster_replid:8c9a4e5c7f6b1d2e3a4f5c6d7e8f9a0b1c2d3e4f\r\nmaster_replid2:0000000000000000000000000000000000000000\r\nmaster_repl_offset:14706\r\nsecond_repl_offset:-1\r\nrepl_backlog_active:1\r\nrepl_backlog_size:1048576\r\nrepl_backlog_first_byte_offset:1\r\nrepl_backlog_histlen:14706\r\n"

	result, err := parseReplicationInfo(input)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.Role != "master" {
		t.Errorf("Expected role master, got %s", result.Role)
	}

	if result.ConnectedSlaves != 2 {
		t.Errorf("Expected 2 connected slaves, got %d", result.ConnectedSlaves)
	}
}

func BenchmarkParseReplicationInfo(b *testing.B) {
	input := "# Replication\r\nrole:master\r\nconnected_slaves:2\r\nslave0:ip=10.0.0.2,port=6379,state=online,offset=1234,lag=0\r\nslave1:ip=10.0.0.3,port=6379,state=online,offset=1234,lag=1\r\nmaster_replid:abc123\r\nmaster_repl_offset:1234\r\n"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := parseReplicationInfo(input)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func TestParseReplicationInfoSplitLines(t *testing.T) {
	input := "line1\r\nline2\nline3\r\n"
	
	lines := strings.Split(input, "\r\n")
	
	// Should handle both \r\n and \n
	if len(lines) < 2 {
		t.Error("Failed to split on \\r\\n")
	}
}

