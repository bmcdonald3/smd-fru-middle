package pipeline

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/benmcdonald/smd-fru-middle/internal/checkpoint"
	"github.com/benmcdonald/smd-fru-middle/internal/config"
	"github.com/benmcdonald/smd-fru-middle/internal/fru"
	"github.com/benmcdonald/smd-fru-middle/internal/models"
	"github.com/benmcdonald/smd-fru-middle/internal/redfish"
	"github.com/benmcdonald/smd-fru-middle/internal/secrets"
	"github.com/benmcdonald/smd-fru-middle/internal/secretsruntime"
	"github.com/benmcdonald/smd-fru-middle/internal/smd"
)

type Service struct {
	cfg        config.Config
	fruClient  *fru.Client
	redfish    *redfish.Client
	smdClient  *smd.Client
	checkpoint *checkpoint.Store
}

func NewService(cfg config.Config) *Service {
	return &Service{
		cfg:        cfg,
		fruClient:  fru.NewClient(cfg.FRUBaseURL, cfg.HTTPTimeout),
		redfish:    redfish.NewClient(cfg.HTTPTimeout),
		smdClient:  smd.NewClient(cfg.SMDBaseURL, cfg.HTTPTimeout),
		checkpoint: checkpoint.New(cfg.CheckpointPath),
	}
}

func (s *Service) Run(ctx context.Context) error {
	if err := s.runCycle(ctx); err != nil {
		log.Printf("initial cycle failed: %v", err)
	}

	ticker := time.NewTicker(s.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.runCycle(ctx); err != nil {
				log.Printf("cycle failed: %v", err)
			}
		}
	}
}

func (s *Service) runCycle(ctx context.Context) error {
	mark, err := s.checkpoint.Load()
	if err != nil {
		return err
	}

	devices, err := s.fruClient.ListDevices(ctx)
	if err != nil {
		return err
	}

	changed := fru.FilterChanged(devices, mark)
	if len(changed) == 0 {
		log.Printf("cycle complete: no changed records")
		return nil
	}

	processed := 0
	skipped := 0
	failed := 0

	for _, device := range changed {
		candidate, ok := s.candidateFromDevice(device)
		if !ok {
			skipped++
			mark = models.Watermark{UpdatedAt: device.Metadata.UpdatedAt, UID: device.Metadata.UID}
			continue
		}

		if err := s.processCandidate(ctx, candidate); err != nil {
			failed++
			log.Printf("candidate %s failed: %v", candidate.XName, err)
			mark = models.Watermark{UpdatedAt: device.Metadata.UpdatedAt, UID: device.Metadata.UID}
			continue
		}

		processed++
		mark = models.Watermark{UpdatedAt: device.Metadata.UpdatedAt, UID: device.Metadata.UID}
	}

	if err := s.checkpoint.Save(mark); err != nil {
		return fmt.Errorf("save checkpoint: %w", err)
	}

	log.Printf("cycle complete: total=%d processed=%d skipped=%d failed=%d", len(changed), processed, skipped, failed)
	return nil
}

func (s *Service) candidateFromDevice(device models.Device) (models.Candidate, bool) {
	props := device.Spec.Properties
	if props == nil {
		return models.Candidate{}, false
	}

	xname := strings.TrimSpace(props[s.cfg.XNamePropertyKey])
	if xname == "" {
		return models.Candidate{}, false
	}

	secretID := strings.TrimSpace(props[s.cfg.SecretIDPropertyKey])
	if secretID == "" {
		return models.Candidate{}, false
	}

	redfishAddr := strings.TrimSpace(props[s.cfg.RedfishAddrKey])
	if redfishAddr == "" {
		if uri := strings.TrimSpace(props["redfish_uri"]); uri != "" {
			redfishAddr = uri
		}
	}

	return models.Candidate{
		UID:            device.Metadata.UID,
		UpdatedAt:      device.Metadata.UpdatedAt,
		XName:          xname,
		SecretID:       secretID,
		RedfishAddress: redfishAddr,
	}, true
}

func (s *Service) processCandidate(ctx context.Context, candidate models.Candidate) error {
	store := secretsruntime.GetStore()
	if store == nil {
		return fmt.Errorf("secret store not initialized")
	}

	raw, err := store.GetSecretByID(candidate.SecretID)
	if err != nil {
		return fmt.Errorf("secret lookup failed for %q: %w", candidate.SecretID, err)
	}

	creds, err := secrets.DecodeCredentials(raw)
	if err != nil {
		return fmt.Errorf("invalid credential payload for %q: %w", candidate.SecretID, err)
	}

	hostname, domain := splitHostAndDomain(candidate.RedfishAddress)
	payload := models.SMDRedfishEndpointPayload{
		SchemaVersion: 1,
		ID:            candidate.XName,
		Hostname:      hostname,
		Domain:        domain,
		User:          creds.Username,
		Password:      creds.Password,
		Enabled:       true,
	}

	if candidate.RedfishAddress != "" {
		systems, managers, discoverErr := s.redfish.Discover(ctx, candidate.RedfishAddress, creds)
		if discoverErr != nil {
			return fmt.Errorf("redfish discovery failed for %q: %w", candidate.XName, discoverErr)
		}
		payload.Systems = systems
		payload.Managers = managers
	}

	if s.cfg.DryRun {
		log.Printf("dry-run: would upsert redfish endpoint id=%s systems=%d managers=%d", payload.ID, len(payload.Systems), len(payload.Managers))
		return nil
	}

	if err := s.smdClient.UpsertRedfishEndpoint(ctx, payload); err != nil {
		return err
	}

	return nil
}

func splitHostAndDomain(address string) (string, string) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", ""
	}

	if !strings.Contains(address, "://") {
		address = "https://" + address
	}

	u, err := url.Parse(address)
	if err != nil {
		return "", ""
	}

	host := u.Hostname()
	if host == "" {
		return "", ""
	}

	parts := strings.SplitN(host, ".", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}

	return parts[0], parts[1]
}
