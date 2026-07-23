package checkpoint

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/benmcdonald/smd-fru-middle/internal/models"
)

type Store struct {
	path string
}

func New(path string) *Store {
	return &Store{path: path}
}

func (s *Store) Load() (models.Watermark, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return models.Watermark{}, nil
		}
		return models.Watermark{}, fmt.Errorf("read checkpoint: %w", err)
	}

	var mark models.Watermark
	if err := json.Unmarshal(data, &mark); err != nil {
		return models.Watermark{}, fmt.Errorf("decode checkpoint: %w", err)
	}

	return mark, nil
}

func (s *Store) Save(mark models.Watermark) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return fmt.Errorf("create checkpoint directory: %w", err)
	}

	encoded, err := json.MarshalIndent(mark, "", "  ")
	if err != nil {
		return fmt.Errorf("encode checkpoint: %w", err)
	}
	encoded = append(encoded, '\n')

	if err := os.WriteFile(s.path, encoded, 0644); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}

	return nil
}
