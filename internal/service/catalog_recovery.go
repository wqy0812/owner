package service

import (
	"context"
	"fmt"
	"time"
)

func (s *CatalogService) recover(ctx context.Context) error {
	if err := s.RecoverActionFileMutations(ctx); err != nil {
		return fmt.Errorf("recover Action file mutations: %w", err)
	}
	if err := s.RecoverComponentImportFiles(ctx); err != nil {
		return fmt.Errorf("recover component import files: %w", err)
	}
	if err := s.store.MarkComponentImageBuildsInterrupted(ctx, time.Now().UTC()); err != nil {
		return fmt.Errorf("recover interrupted image builds: %w", err)
	}
	return nil
}
