package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/benmcdonald/smd-fru-middle/internal/models"
	secretshelper "github.com/benmcdonald/smd-fru-middle/internal/secrets"
)

func main() {
	var (
		secretID  string
		username  string
		password  string
		storePath string
	)

	flag.StringVar(&secretID, "secret-id", "", "Secret identifier consumed by FRU candidate mappings")
	flag.StringVar(&username, "username", "", "BMC username to store")
	flag.StringVar(&password, "password", "", "BMC password to store")
	flag.StringVar(&storePath, "store-path", "secrets.json", "Path to encrypted secrets store JSON file")
	flag.Parse()

	if err := validateInputs(secretID, username, password, storePath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	store, err := secretshelper.OpenStore(storePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open store: %v\n", err)
		os.Exit(1)
	}

	payload := models.Credentials{
		Username: strings.TrimSpace(username),
		Password: strings.TrimSpace(password),
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: marshal payload: %v\n", err)
		os.Exit(1)
	}

	if err := store.StoreSecretByID(strings.TrimSpace(secretID), string(payloadJSON)); err != nil {
		if persistErr := secretshelper.PersistStoreFallback(storePath, store); persistErr != nil {
			fmt.Fprintf(os.Stderr, "error: store secret: %v (fallback failed: %v)\n", err, persistErr)
			os.Exit(1)
		}
	}

	fmt.Printf("Stored credentials for secret-id %q in %s\n", strings.TrimSpace(secretID), storePath)
}

func validateInputs(secretID, username, password, storePath string) error {
	secretID = strings.TrimSpace(secretID)
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	storePath = strings.TrimSpace(storePath)

	if secretID == "" {
		return fmt.Errorf("--secret-id is required")
	}
	if username == "" {
		return fmt.Errorf("--username is required")
	}
	if password == "" {
		return fmt.Errorf("--password is required")
	}
	if storePath == "" {
		return fmt.Errorf("--store-path is required")
	}

	if err := secretshelper.ValidateMasterKey(); err != nil {
		return err
	}

	return nil
}
