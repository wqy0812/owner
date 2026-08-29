package store

import (
	"context"
	"testing"
)

func TestResetAdvancesPublicationGeneration(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.DB().ExecContext(ctx, `UPDATE publication_state SET generation=9 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := database.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	var generation int64
	if err := database.DB().QueryRowContext(ctx, `SELECT generation FROM publication_state WHERE id=1`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if generation != 10 {
		t.Fatalf("publication generation=%d want=10", generation)
	}
}
