package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/satishbabariya/syncmesh/cmd"
	"github.com/satishbabariya/syncmesh/internal/logger"
)

func main() {
	// Initialize logger
	log := logger.New()

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
	if err := cmd.Execute(ctx, nil, log); err != nil {
		log.WithError(err).Fatal("Application failed")
		os.Exit(1)
	}
}
