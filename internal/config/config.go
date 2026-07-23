package config
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultFRUBaseURL      = "http://localhost:8080"
	defaultSMDBaseURL      = "http://localhost:27779"
	defaultPollInterval    = 30 * time.Second
	defaultHTTPTimeout     = 20 * time.Second
	defaultCheckpointPath  = "data/checkpoint.json"
	defaultSecretsFilePath = "secrets.json"
)

type Config struct {
	FRUBaseURL          string
	SMDBaseURL          string
	PollInterval        time.Duration
	HTTPTimeout         time.Duration
	CheckpointPath      string
	SecretsFilePath     string
	XNamePropertyKey    string
	SecretIDPropertyKey string
	RedfishAddrKey      string
	DryRun              bool
}

func Load() (Config, error) {
	cfg := Config{
		FRUBaseURL:          getEnvOrDefault("FRU_MIDDLE_FRU_BASE_URL", defaultFRUBaseURL),
		SMDBaseURL:          getEnvOrDefault("FRU_MIDDLE_SMD_BASE_URL", defaultSMDBaseURL),
		CheckpointPath:      getEnvOrDefault("FRU_MIDDLE_CHECKPOINT_PATH", defaultCheckpointPath),
		SecretsFilePath:     getEnvOrDefault("FRU_MIDDLE_SECRETS_FILE", defaultSecretsFilePath),
		XNamePropertyKey:    getEnvOrDefault("FRU_MIDDLE_XNAME_PROPERTY_KEY", "xname"),
		SecretIDPropertyKey: getEnvOrDefault("FRU_MIDDLE_SECRET_ID_PROPERTY_KEY", "secret_id"),
		RedfishAddrKey:      getEnvOrDefault("FRU_MIDDLE_REDFISH_ADDR_PROPERTY_KEY", "redfish_address"),
	}

	pollInterval, err := parseDurationOrDefault("FRU_MIDDLE_POLL_INTERVAL", defaultPollInterval)
	if err != nil {
		return Config{}, err
	}
	cfg.PollInterval = pollInterval

	httpTimeout, err := parseDurationOrDefault("FRU_MIDDLE_HTTP_TIMEOUT", defaultHTTPTimeout)
	if err != nil {
		return Config{}, err
	}
	cfg.HTTPTimeout = httpTimeout

	dryRun, err := parseBoolOrDefault("FRU_MIDDLE_DRY_RUN", false)
	if err != nil {
		return Config{}, err
	}
	cfg.DryRun = dryRun

	if strings.TrimSpace(cfg.FRUBaseURL) == "" {
		return Config{}, fmt.Errorf("FRU_MIDDLE_FRU_BASE_URL must not be empty")
	}
	if strings.TrimSpace(cfg.SMDBaseURL) == "" {
		return Config{}, fmt.Errorf("FRU_MIDDLE_SMD_BASE_URL must not be empty")
	}

	return cfg, nil
}

func getEnvOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func parseDurationOrDefault(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive", key)
	}
	return d, nil
}

func parseBoolOrDefault(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}

	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a valid boolean: %w", key, err)
	}
	return value, nil
}
