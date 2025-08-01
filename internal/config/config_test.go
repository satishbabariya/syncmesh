package config

import (
	"testing"
)

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		config    *Config
		expectErr bool
	}{
		{
			name: "valid config",
			config: &Config{
				Server: ServerConfig{
					Port:     8080,
					GRPCPort: 8081,
				},
				Sync: SyncConfig{
					Interval:           30,
					BatchSize:          100,
					ConflictResolution: "timestamp",
					ChecksumAlgorithm:  "sha256",
				},
				Cluster: ClusterConfig{
					Mode: "raft",
				},
			},
			expectErr: false,
		},
		{
			name: "invalid port",
			config: &Config{
				Server: ServerConfig{
					Port:     -1,
					GRPCPort: 8081,
				},
			},
			expectErr: true,
		},
		{
			name: "invalid conflict resolution",
			config: &Config{
				Server: ServerConfig{
					Port:     8080,
					GRPCPort: 8081,
				},
				Sync: SyncConfig{
					Interval:           30,
					BatchSize:          100,
					ConflictResolution: "invalid",
					ChecksumAlgorithm:  "sha256",
				},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.expectErr {
				t.Errorf("Validate() error = %v, expectErr %v", err, tt.expectErr)
			}
		})
	}
}

func TestSetDefaults(t *testing.T) {
	// This would test the setDefaults function
	// In a real implementation, you would test that all defaults are set correctly
	t.Log("setDefaults test placeholder")
}
