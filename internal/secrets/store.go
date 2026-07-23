package secrets

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	magellan "github.com/OpenCHAMI/magellan/pkg/secrets"
	"github.com/benmcdonald/smd-fru-middle/internal/models"
)

func ValidateMasterKey() error {
	masterKey := strings.TrimSpace(os.Getenv("MASTER_KEY"))
	if len(masterKey) != 64 {
		return fmt.Errorf("MASTER_KEY must be a 64-character hex string")
	}

	decoded, err := hex.DecodeString(masterKey)
	if err != nil {
		return fmt.Errorf("MASTER_KEY must be valid hex: %w", err)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("MASTER_KEY must decode to 32 bytes for AES-256")
	}

	return nil
}

func OpenStore(storePath string) (magellan.SecretStore, error) {
	store, err := magellan.OpenStore(storePath)
	if err == nil {
		return store, nil
	}

	if !strings.Contains(strings.ToLower(err.Error()), "file already closed") {
		return nil, err
	}

	masterKey := strings.TrimSpace(os.Getenv("MASTER_KEY"))
	tmpFile, tmpErr := os.CreateTemp("", "smd-fru-middle-secret-store-*.json")
	if tmpErr != nil {
		return nil, fmt.Errorf("create temp fallback store file: %w", tmpErr)
	}
	tmpPath := tmpFile.Name()
	if closeErr := tmpFile.Close(); closeErr != nil {
		return nil, fmt.Errorf("close temp fallback store file: %w", closeErr)
	}
	if removeErr := os.Remove(tmpPath); removeErr != nil {
		return nil, fmt.Errorf("prepare temp fallback store path: %w", removeErr)
	}
	defer os.Remove(tmpPath)

	fallbackStore, fallbackErr := magellan.NewLocalSecretStore(masterKey, tmpPath, true)
	if fallbackErr != nil {
		return nil, fmt.Errorf("create fallback local secret store: %w", fallbackErr)
	}

	if content, readErr := os.ReadFile(storePath); readErr == nil && len(content) > 0 {
		secretMap := make(map[string]string)
		if unmarshalErr := json.Unmarshal(content, &secretMap); unmarshalErr != nil {
			return nil, fmt.Errorf("decode encrypted secrets JSON: %w", unmarshalErr)
		}
		fallbackStore.Secrets = secretMap
	}

	return fallbackStore, nil
}

func PersistStoreFallback(storePath string, store magellan.SecretStore) error {
	localStore, ok := store.(*magellan.LocalSecretStore)
	if !ok {
		return fmt.Errorf("unsupported secret store type %T", store)
	}

	encoded, err := json.MarshalIndent(localStore.Secrets, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal encrypted secrets map: %w", err)
	}

	encoded = append(encoded, '\n')
	if err := os.WriteFile(storePath, encoded, 0644); err != nil {
		return fmt.Errorf("write encrypted secrets file: %w", err)
	}

	return nil
}

func DecodeCredentials(raw string) (models.Credentials, error) {
	var creds models.Credentials
	if err := json.Unmarshal([]byte(raw), &creds); err != nil {
		return models.Credentials{}, fmt.Errorf("decode credential payload: %w", err)
	}

	creds.Username = strings.TrimSpace(creds.Username)
	creds.Password = strings.TrimSpace(creds.Password)
	if creds.Username == "" || creds.Password == "" {
		return models.Credentials{}, fmt.Errorf("credential payload must include non-empty username and password")
	}

	return creds, nil
}
