package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/benmcdonald/smd-fru-middle/internal/config"
	"github.com/benmcdonald/smd-fru-middle/internal/pipeline"
	"github.com/benmcdonald/smd-fru-middle/internal/secrets"
	"github.com/benmcdonald/smd-fru-middle/internal/secretsruntime"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if err := secrets.ValidateMasterKey(); err != nil {
		log.Fatalf("validate master key: %v", err)
	}

	store, err := secrets.OpenStore(cfg.SecretsFilePath)
	if err != nil {
		log.Fatalf("open secret store: %v", err)
	}

	if err := secretsruntime.SetStore(store); err != nil {
		log.Fatalf("initialize secret runtime store: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	svc := pipeline.NewService(cfg)
	if err := svc.Run(ctx); err != nil && err != context.Canceled {
		log.Fatalf("service exited with error: %v", err)
	}
}
