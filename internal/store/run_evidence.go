package store

import (
	"context"
	"database/sql"
	"errors"

	"codex/platform-demo/internal/domain"
)

func (s *Store) HasSuccessfulComponentTestAction(ctx context.Context, releaseID string, action domain.ActionKind, releaseSpecDigest string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM runs
  WHERE kind='component_test' AND component_release_id=? AND action_kind=? AND status='succeeded'
    AND component_spec_digest=?
)`, releaseID, action, releaseSpecDigest).Scan(&found)
	return found != 0, err
}

func (s *Store) HasSuccessfulComponentInstallVerification(ctx context.Context, releaseID, releaseSpecDigest string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM runs
  WHERE kind='component_test' AND component_release_id=? AND status='succeeded'
    AND component_spec_digest=?
    AND component_evidence_kind='install_verify'
)`, releaseID, releaseSpecDigest).Scan(&found)
	return found != 0, err
}

func (s *Store) HasSuccessfulComponentRollbackVerification(ctx context.Context, releaseID, releaseSpecDigest string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `
SELECT EXISTS(
  SELECT 1 FROM runs
  WHERE kind='component_test' AND component_release_id=? AND action_kind='rollback' AND status='succeeded'
    AND component_spec_digest=?
    AND component_evidence_kind='rollback_verify'
)`, releaseID, releaseSpecDigest).Scan(&found)
	return found != 0, err
}

func (s *Store) SuccessfulComponentEvidenceRunIDs(ctx context.Context, releaseID, releaseSpecDigest string) (string, string, error) {
	return s.successfulComponentEvidenceRunIDs(ctx, releaseID, releaseSpecDigest, "", "")
}

func (s *Store) SuccessfulComponentEvidenceRunIDsForRuntime(ctx context.Context, releaseID, releaseSpecDigest, runtime, version string) (string, string, error) {
	return s.successfulComponentEvidenceRunIDs(ctx, releaseID, releaseSpecDigest, runtime, version)
}

func (s *Store) successfulComponentEvidenceRunIDs(ctx context.Context, releaseID, releaseSpecDigest, runtime, version string) (string, string, error) {
	installID, err := s.lookupComponentEvidence(ctx, releaseID, releaseSpecDigest, runtime, version, "install_verify")
	if err != nil {
		return "", "", err
	}
	rollbackID, err := s.lookupComponentEvidence(ctx, releaseID, releaseSpecDigest, runtime, version, "rollback_verify")
	return installID, rollbackID, err
}

func (s *Store) SuccessfulComponentEvolutionEvidenceRunID(ctx context.Context, releaseID, releaseSpecDigest string) (string, error) {
	return s.successfulComponentEvolutionEvidenceRunID(ctx, releaseID, releaseSpecDigest, "", "")
}

func (s *Store) SuccessfulComponentEvolutionEvidenceRunIDForRuntime(ctx context.Context, releaseID, releaseSpecDigest, runtime, version string) (string, error) {
	return s.successfulComponentEvolutionEvidenceRunID(ctx, releaseID, releaseSpecDigest, runtime, version)
}

func (s *Store) successfulComponentEvolutionEvidenceRunID(ctx context.Context, releaseID, releaseSpecDigest, runtime, version string) (string, error) {
	return s.lookupComponentEvidence(ctx, releaseID, releaseSpecDigest, runtime, version, "evolution_round_trip")
}

func (s *Store) lookupComponentEvidence(ctx context.Context, releaseID, digest, runtime, version, evidence string) (string, error) {
	query, args := componentEvidenceQuery(releaseID, digest, runtime, version, evidence)
	var id string
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// Equality predicates match the generated-column indexes. Select one newest
// candidate per accepted evidence kind so rollback only sorts at most two rows,
// even when a contract has thousands of successful historical tests.
func componentEvidenceQuery(releaseID, digest, runtime, version, evidence string) (string, []any) {
	query := `SELECT id,evidence_at,created_at FROM runs
WHERE kind='component_test' AND status='succeeded'
  AND component_release_id=? AND component_spec_digest=?`
	args := []any{releaseID, digest}
	if runtime != "" {
		query += ` AND runtime_name=? AND runtime_version=?`
		args = append(args, runtime, version)
	}
	query += ` AND component_evidence_kind=?
ORDER BY evidence_at DESC,created_at DESC,id DESC LIMIT 1`
	args = append(args, evidence)
	if evidence != "rollback_verify" {
		return `SELECT id FROM (` + query + `)`, args
	}
	secondArgs := append([]any(nil), args...)
	secondArgs[len(secondArgs)-1] = "rollback_self_verify"
	return `SELECT id FROM (
SELECT * FROM (` + query + `)
UNION ALL
SELECT * FROM (` + query + `)
) ORDER BY evidence_at DESC,created_at DESC,id DESC LIMIT 1`, append(args, secondArgs...)
}
