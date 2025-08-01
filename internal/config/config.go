package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for the file sync cluster
type Config struct {
	// Application settings
	ConfigFile string `mapstructure:"config_file" yaml:"config_file"`
	LogLevel   string `mapstructure:"log_level" yaml:"log_level"`
	NodeID     string `mapstructure:"node_id" yaml:"node_id"`

	// Server settings
	Server ServerConfig `mapstructure:"server" yaml:"server"`

	// Sync settings
	Sync SyncConfig `mapstructure:"sync" yaml:"sync"`

	// Cluster settings
	Cluster ClusterConfig `mapstructure:"cluster" yaml:"cluster"`

	// Security settings
	Security SecurityConfig `mapstructure:"security" yaml:"security"`

	// Monitoring settings
	Monitoring MonitoringConfig `mapstructure:"monitoring" yaml:"monitoring"`

	// Docker settings
	Docker DockerConfig `mapstructure:"docker" yaml:"docker"`

	// Storage settings
	Storage StorageConfig `mapstructure:"storage" yaml:"storage"`
}

type ServerConfig struct {
	Host         string        `mapstructure:"host" yaml:"host"`
	Port         int           `mapstructure:"port" yaml:"port"`
	GRPCPort     int           `mapstructure:"grpc_port" yaml:"grpc_port"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout" yaml:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout" yaml:"write_timeout"`
	IdleTimeout  time.Duration `mapstructure:"idle_timeout" yaml:"idle_timeout"`
}

type SyncConfig struct {
	Interval           time.Duration `mapstructure:"interval" yaml:"interval"`
	BatchSize          int           `mapstructure:"batch_size" yaml:"batch_size"`
	MaxRetries         int           `mapstructure:"max_retries" yaml:"max_retries"`
	RetryBackoff       time.Duration `mapstructure:"retry_backoff" yaml:"retry_backoff"`
	ConflictResolution string        `mapstructure:"conflict_resolution" yaml:"conflict_resolution"` // "timestamp", "size", "manual"
	ChecksumAlgorithm  string        `mapstructure:"checksum_algorithm" yaml:"checksum_algorithm"`   // "sha256", "md5"
	CompressionEnabled bool          `mapstructure:"compression_enabled" yaml:"compression_enabled"`
	CompressionLevel   int           `mapstructure:"compression_level" yaml:"compression_level"`
	ExcludePatterns    []string      `mapstructure:"exclude_patterns" yaml:"exclude_patterns"`
	IncludePatterns    []string      `mapstructure:"include_patterns" yaml:"include_patterns"`
}

type ClusterConfig struct {
	Mode             string        `mapstructure:"mode" yaml:"mode"` // "raft", "consul", "etcd"
	RaftDataDir      string        `mapstructure:"raft_data_dir" yaml:"raft_data_dir"`
	BindAddr         string        `mapstructure:"bind_addr" yaml:"bind_addr"`
	AdvertiseAddr    string        `mapstructure:"advertise_addr" yaml:"advertise_addr"`
	Bootstrap        bool          `mapstructure:"bootstrap" yaml:"bootstrap"`
	JoinAddresses    []string      `mapstructure:"join_addresses" yaml:"join_addresses"`
	HeartbeatTimeout time.Duration `mapstructure:"heartbeat_timeout" yaml:"heartbeat_timeout"`
	ElectionTimeout  time.Duration `mapstructure:"election_timeout" yaml:"election_timeout"`

	// Consul configuration
	ConsulAddress string `mapstructure:"consul_address" yaml:"consul_address"`
	ConsulToken   string `mapstructure:"consul_token" yaml:"consul_token"`

	// etcd configuration
	EtcdEndpoints []string `mapstructure:"etcd_endpoints" yaml:"etcd_endpoints"`
	EtcdUsername  string   `mapstructure:"etcd_username" yaml:"etcd_username"`
	EtcdPassword  string   `mapstructure:"etcd_password" yaml:"etcd_password"`
}

type SecurityConfig struct {
	TLSEnabled    bool     `mapstructure:"tls_enabled" yaml:"tls_enabled"`
	TLSCertFile   string   `mapstructure:"tls_cert_file" yaml:"tls_cert_file"`
	TLSKeyFile    string   `mapstructure:"tls_key_file" yaml:"tls_key_file"`
	TLSCAFile     string   `mapstructure:"tls_ca_file" yaml:"tls_ca_file"`
	AuthEnabled   bool     `mapstructure:"auth_enabled" yaml:"auth_enabled"`
	AuthTokens    []string `mapstructure:"auth_tokens" yaml:"auth_tokens"`
	JWTSecret     string   `mapstructure:"jwt_secret" yaml:"jwt_secret"`
	EncryptionKey string   `mapstructure:"encryption_key" yaml:"encryption_key"`
}

type MonitoringConfig struct {
	Enabled           bool   `mapstructure:"enabled" yaml:"enabled"`
	MetricsPath       string `mapstructure:"metrics_path" yaml:"metrics_path"`
	MetricsPort       int    `mapstructure:"metrics_port" yaml:"metrics_port"`
	HealthPath        string `mapstructure:"health_path" yaml:"health_path"`
	PrometheusEnabled bool   `mapstructure:"prometheus_enabled" yaml:"prometheus_enabled"`

	// Health check settings
	HealthCheckInterval time.Duration `mapstructure:"health_check_interval" yaml:"health_check_interval"`
	HealthCheckTimeout  time.Duration `mapstructure:"health_check_timeout" yaml:"health_check_timeout"`
}

type DockerConfig struct {
	SocketPath       string        `mapstructure:"socket_path" yaml:"socket_path"`
	APIVersion       string        `mapstructure:"api_version" yaml:"api_version"`
	WatchedVolumes   []string      `mapstructure:"watched_volumes" yaml:"watched_volumes"`
	VolumePrefix     string        `mapstructure:"volume_prefix" yaml:"volume_prefix"`
	ContainerTimeout time.Duration `mapstructure:"container_timeout" yaml:"container_timeout"`
}

type StorageConfig struct {
	DataDir         string        `mapstructure:"data_dir" yaml:"data_dir"`
	TempDir         string        `mapstructure:"temp_dir" yaml:"temp_dir"`
	MaxFileSize     int64         `mapstructure:"max_file_size" yaml:"max_file_size"`
	DiskSpaceLimit  int64         `mapstructure:"disk_space_limit" yaml:"disk_space_limit"`
	CleanupEnabled  bool          `mapstructure:"cleanup_enabled" yaml:"cleanup_enabled"`
	CleanupInterval time.Duration `mapstructure:"cleanup_interval" yaml:"cleanup_interval"`
}

// Load loads configuration from environment variables and default values
func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")
	viper.AddConfigPath("/etc/syncmesh")

	// Set default values
	setDefaults()

	// Environment variable support
	viper.SetEnvPrefix("SYNCMESH")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// Try to read config file
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// Generate node ID if not set
	if config.NodeID == "" {
		hostname, _ := os.Hostname()
		config.NodeID = fmt.Sprintf("%s-%d", hostname, os.Getpid())
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &config, nil
}

// LoadFromFile loads configuration from a specific file
func LoadFromFile(filename string) (*Config, error) {
	viper.SetConfigFile(filename)

	setDefaults()

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("error reading config file %s: %w", filename, err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return &config, nil
}

func setDefaults() {
	// Application defaults
	viper.SetDefault("log_level", "info")

	// Server defaults
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.grpc_port", 8081)
	viper.SetDefault("server.read_timeout", "30s")
	viper.SetDefault("server.write_timeout", "30s")
	viper.SetDefault("server.idle_timeout", "60s")

	// Sync defaults
	viper.SetDefault("sync.interval", "30s")
	viper.SetDefault("sync.batch_size", 100)
	viper.SetDefault("sync.max_retries", 3)
	viper.SetDefault("sync.retry_backoff", "5s")
	viper.SetDefault("sync.conflict_resolution", "timestamp")
	viper.SetDefault("sync.checksum_algorithm", "sha256")
	viper.SetDefault("sync.compression_enabled", true)
	viper.SetDefault("sync.compression_level", 6)
	viper.SetDefault("sync.exclude_patterns", []string{".tmp", ".log", ".lock"})

	// Cluster defaults
	viper.SetDefault("cluster.mode", "raft")
	viper.SetDefault("cluster.raft_data_dir", "./data/raft")
	viper.SetDefault("cluster.bind_addr", "127.0.0.1:8082")
	viper.SetDefault("cluster.bootstrap", false)
	viper.SetDefault("cluster.heartbeat_timeout", "1s")
	viper.SetDefault("cluster.election_timeout", "1s")

	// Security defaults
	viper.SetDefault("security.tls_enabled", false)
	viper.SetDefault("security.auth_enabled", false)

	// Monitoring defaults
	viper.SetDefault("monitoring.enabled", true)
	viper.SetDefault("monitoring.metrics_path", "/metrics")
	viper.SetDefault("monitoring.metrics_port", 9090)
	viper.SetDefault("monitoring.health_path", "/health")
	viper.SetDefault("monitoring.prometheus_enabled", true)
	viper.SetDefault("monitoring.health_check_interval", "30s")
	viper.SetDefault("monitoring.health_check_timeout", "5s")

	// Docker defaults
	viper.SetDefault("docker.socket_path", "/var/run/docker.sock")
	viper.SetDefault("docker.api_version", "1.41")
	viper.SetDefault("docker.volume_prefix", "sync-")
	viper.SetDefault("docker.container_timeout", "30s")

	// Storage defaults
	viper.SetDefault("storage.data_dir", "./data")
	viper.SetDefault("storage.temp_dir", "/tmp/syncmesh")
	viper.SetDefault("storage.max_file_size", 1073741824)     // 1GB
	viper.SetDefault("storage.disk_space_limit", 10737418240) // 10GB
	viper.SetDefault("storage.cleanup_enabled", true)
	viper.SetDefault("storage.cleanup_interval", "1h")
}

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	if c.Server.GRPCPort <= 0 || c.Server.GRPCPort > 65535 {
		return fmt.Errorf("invalid gRPC port: %d", c.Server.GRPCPort)
	}

	if c.Sync.Interval <= 0 {
		return fmt.Errorf("sync interval must be positive")
	}

	if c.Sync.BatchSize <= 0 {
		return fmt.Errorf("sync batch size must be positive")
	}

	if c.Sync.ConflictResolution != "timestamp" && c.Sync.ConflictResolution != "size" && c.Sync.ConflictResolution != "manual" {
		return fmt.Errorf("invalid conflict resolution strategy: %s", c.Sync.ConflictResolution)
	}

	if c.Sync.ChecksumAlgorithm != "sha256" && c.Sync.ChecksumAlgorithm != "md5" {
		return fmt.Errorf("invalid checksum algorithm: %s", c.Sync.ChecksumAlgorithm)
	}

	if c.Cluster.Mode != "raft" && c.Cluster.Mode != "consul" && c.Cluster.Mode != "etcd" {
		return fmt.Errorf("invalid cluster mode: %s", c.Cluster.Mode)
	}

	if c.Security.TLSEnabled {
		if c.Security.TLSCertFile == "" || c.Security.TLSKeyFile == "" {
			return fmt.Errorf("TLS cert and key files must be specified when TLS is enabled")
		}
	}

	return nil
}
