package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/satishbabariya/syncmesh/cmd"
	"github.com/satishbabariya/syncmesh/internal/config"
	"github.com/satishbabariya/syncmesh/internal/logger"
	"github.com/sirupsen/logrus"
)

func main() {
	// Initialize logger
	log := logger.New()

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.WithError(err).Fatal("Failed to load configuration")
	}

	// Set log level from config
	level, err := logrus.ParseLevel(cfg.LogLevel)
	if err != nil {
		log.WithError(err).Warn("Invalid log level, using info")
		level = logrus.InfoLevel
	}
	log.SetLevel(level)

	// Create context that cancels on interrupt
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Info("Received shutdown signal, gracefully shutting down...")
		cancel()
	}()

	// Execute root command
	if err := cmd.Execute(ctx, cfg, log); err != nil {
		log.WithError(err).Fatal("Application failed")
		os.Exit(1)
	}
}
