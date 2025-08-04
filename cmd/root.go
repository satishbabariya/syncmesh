package cmd

import (
	"context"
	"fmt"

	"github.com/satishbabariya/syncmesh/internal/config"
	"github.com/satishbabariya/syncmesh/internal/server"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var (
	configFile string
	rootCmd    = &cobra.Command{
		Use:   "syncmesh",
		Short: "Enterprise distributed file synchronization mesh for Docker volumes",
		Long: `SyncMesh - A high-performance, enterprise-grade distributed file 
synchronization system for Docker volumes with real-time sync, cluster 
coordination, and comprehensive monitoring.`,
		RunE: runServer,
	}
)

func init() {
	rootCmd.PersistentFlags().StringVar(&configFile, "config", "", "config file (default is ./config.yaml)")
}

func Execute(ctx context.Context, cfg *config.Config, log *logrus.Logger) error {
	return rootCmd.ExecuteContext(ctx)
}

func runServer(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Override config file if provided
	if configFile != "" {
		cfg.ConfigFile = configFile
		cfg, err = config.LoadFromFile(configFile)
		if err != nil {
			return fmt.Errorf("failed to load config from file %s: %w", configFile, err)
		}
	}

	// Create and start server
	srv := server.New(cfg)

	return srv.Start(ctx)
}
