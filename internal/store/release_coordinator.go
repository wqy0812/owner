package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
)

// ReleasePublicationGuard binds publication to the exact database generation
// and canonical definition that the service validated. Status and candidate
// are included because both participate in dependency and ownership handoff
// rules even though they are not part of the content digest.
type ReleasePublicationGuard struct {
	ReleaseID             string
	PublicationGeneration int64
	SpecDigest            string
	Status                domain.ReleaseStatus
	Candidate             bool
	ReviewStatus          domain.ReleaseReviewStatus
	ReviewContractDigest  string
	Missing               bool
}

type ScenarioPublicationGuard struct {
	RevisionID            string
	PublicationGeneration int64
	SpecDigest            string
}

func (s *Store) GetPublicationEpoch(ctx context.Context) (int64, error) {
	var generation int64
	err := s.db.QueryRowContext(ctx, `SELECT generation FROM publication_state WHERE id=1`).Scan(&generation)
	return generation, err
}

func verifyReleasePublicationGuards(ctx context.Context, tx queryer, guards []ReleasePublicationGuard) (map[string]domain.ComponentRelease, error) {
	current := make(map[string]domain.ComponentRelease, len(guards))
	seen := make(map[string]bool, len(guards))
	for _, guard := range guards {
		if guard.ReleaseID == "" {
			return nil, fmt.Errorf("%w: incomplete release publication guard", domain.ErrInvalid)
		}
		if seen[guard.ReleaseID] {
			return nil, fmt.Errorf("%w: duplicate release publication guard %s", domain.ErrInvalid, guard.ReleaseID)
		}
		seen[guard.ReleaseID] = true
		if guard.Missing {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM component_releases WHERE id=?)`, guard.ReleaseID).Scan(&exists); err != nil {
				return nil, err
			}
			if exists != 0 {
				return nil, fmt.Errorf("%w: referenced release %s appeared after publication validation started", domain.ErrConflict, guard.ReleaseID)
			}
			continue
		}
		if guard.PublicationGeneration <= 0 || guard.SpecDigest == "" {
			return nil, fmt.Errorf("%w: incomplete release publication guard", domain.ErrInvalid)
		}
		release, err := getComponentRelease(ctx, tx, guard.ReleaseID)
		if err != nil {
			return nil, err
		}
		if release.PublicationGeneration != guard.PublicationGeneration ||
			domain.ComponentReleaseSpecDigest(release) != guard.SpecDigest ||
			release.Status != guard.Status || release.Candidate != guard.Candidate || release.Review.Status != guard.ReviewStatus || release.Review.ContractDigest != guard.ReviewContractDigest {
			return nil, fmt.Errorf("%w: release %s changed after publication validation", domain.ErrConflict, guard.ReleaseID)
		}
		current[guard.ReleaseID] = release
	}
	return current, nil
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func advancePublicationEpoch(ctx context.Context, tx execer, expected int64) error {
	result, err := tx.ExecContext(ctx, `UPDATE publication_state SET generation=generation+1 WHERE id=1 AND generation=?`, expected)
	if err != nil {
		return publicationWriteError(err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("%w: the released dependency graph changed during publication validation", domain.ErrConflict)
	}
	return nil
}

func publicationWriteError(err error) error {
	if err != nil && (strings.Contains(strings.ToLower(err.Error()), "database is locked") || strings.Contains(strings.ToUpper(err.Error()), "SQLITE_BUSY")) {
		return fmt.Errorf("%w: publication state changed concurrently: %v", domain.ErrConflict, err)
	}
	return err
}

// PublishComponentRelease is the final persistence boundary for standalone
// publication. Every definition/dependency guard and the global released-graph
// epoch are checked inside the same transaction as the status transition.
func (s *Store) PublishComponentRelease(ctx context.Context, id string, expectedEpoch int64, guards []ReleasePublicationGuard, at time.Time) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := verifyReleasePublicationGuards(ctx, tx, guards)
	if err != nil {
		return err
	}
	release, ok := current[id]
	if !ok || release.Status != domain.ReleaseDraft || release.Review.Status != domain.ReleaseReviewApproved || release.Review.ContractDigest != domain.ComponentReleaseSpecDigest(release) {
		return fmt.Errorf("%w: only a guarded draft release can be published", domain.ErrConflict)
	}
	if err := advancePublicationEpoch(ctx, tx, expectedEpoch); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE component_releases SET status='released',candidate=0,released_at=?,publication_generation=publication_generation+1 WHERE id=? AND status='draft' AND publication_generation=?`, timeText(at), id, release.PublicationGeneration)
	if err != nil {
		return publicationWriteError(err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("%w: release changed before guarded publish", domain.ErrConflict)
	}
	return publicationWriteError(tx.Commit())
}

func releaseLocksFromRunSnapshot(snapshot map[string]any) (map[string]string, error) {
	rawSteps, ok := snapshot["steps"].([]any)
	if !ok || len(rawSteps) == 0 {
		return nil, fmt.Errorf("%w: scenario test has no locked steps", domain.ErrConflict)
	}
	if parents, ok := snapshot["parentSteps"].([]any); ok {
		rawSteps = append(append([]any{}, rawSteps...), parents...)
	}
	locks := map[string]string{}
	for _, raw := range rawSteps {
		step, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: scenario test contains an invalid locked step", domain.ErrConflict)
		}
		if step["sourceType"] == "scenario_acceptance" {
			continue
		}
		releaseID, _ := step["releaseId"].(string)
		digest, _ := step["releaseSpecDigest"].(string)
		if releaseID == "" || digest == "" {
			return nil, fmt.Errorf("%w: scenario test step is missing its release definition digest", domain.ErrConflict)
		}
		if existing, duplicate := locks[releaseID]; duplicate && existing != digest {
			return nil, fmt.Errorf("%w: scenario test locked conflicting definitions for release %s", domain.ErrConflict, releaseID)
		}
		locks[releaseID] = digest
	}
	return locks, nil
}

func getRunRecord(ctx context.Context, q queryer, id string) (domain.Run, error) {
	run, err := scanRun(q.QueryRowContext(ctx, runSelect+` WHERE id=?`, id))
	return run, mapSQLError(err)
}

func scenarioTestEvidenceMatches(ctx context.Context, tx queryer, run domain.Run, revision domain.ScenarioRevision) (map[string]domain.ComponentRelease, error) {
	if run.Kind != domain.RunScenarioTest || run.Status != domain.RunSucceeded || run.ScenarioRevisionID != revision.ID {
		return nil, fmt.Errorf("%w: Run is not successful evidence for this scenario revision", domain.ErrConflict)
	}
	if revision.DigestVersion >= domain.ScenarioDigestVersion {
		mode, _ := run.InputSnapshot["executionMode"].(string)
		if mode != "install" && mode != "upgrade" {
			return nil, domain.ErrConflict
		}
		if err := validateScenarioAcceptanceEvidence(ctx, tx, run, revision); err != nil {
			return nil, err
		}
	}
	if err := validateScenarioAdaptationRead(ctx, tx, revision); err != nil {
		return nil, err
	}
	lockedScenarioDigest, _ := run.InputSnapshot["scenarioRevisionSpecDigest"].(string)
	if lockedScenarioDigest == "" || lockedScenarioDigest != domain.ScenarioRevisionSpecDigest(revision) {
		return nil, fmt.Errorf("%w: scenario test evidence belongs to a different graph definition", domain.ErrConflict)
	}
	locks, err := releaseLocksFromRunSnapshot(run.InputSnapshot)
	if err != nil {
		return nil, err
	}
	current := make(map[string]domain.ComponentRelease, len(locks))
	for releaseID, digest := range locks {
		release, getErr := getComponentRelease(ctx, tx, releaseID)
		if getErr != nil {
			return nil, getErr
		}
		if domain.ComponentReleaseSpecDigest(release) != digest {
			return nil, fmt.Errorf("%w: scenario test evidence for release %s is stale", domain.ErrConflict, releaseID)
		}
		current[releaseID] = release
	}
	for _, node := range revision.Graph.Nodes {
		release, covered := current[node.ReleaseID]
		if !covered {
			return nil, fmt.Errorf("%w: scenario test evidence does not cover graph release %s", domain.ErrConflict, node.ReleaseID)
		}
		if release.Status != domain.ReleaseReleased && (release.Status != domain.ReleaseDraft || !release.Candidate) {
			return nil, fmt.Errorf("%w: tested graph release %s is no longer released or shared as a candidate", domain.ErrConflict, node.ReleaseID)
		}
	}
	return current, nil
}

// PromoteScenarioRevisionFromRun records test_passed only when the successful
// Run still matches both the Scenario graph and every locked Release digest.
// A stale success resets the Revision to Draft in the same transaction.
func (s *Store) PromoteScenarioRevisionFromRun(ctx context.Context, runID string, at time.Time) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	run, err := getRunRecord(ctx, tx, runID)
	if err != nil {
		return err
	}
	revision, err := getScenarioRevision(ctx, tx, run.ScenarioRevisionID)
	if err != nil {
		return err
	}
	if revision.Status != domain.RevisionTesting {
		return fmt.Errorf("%w: scenario revision is no longer testing", domain.ErrConflict)
	}
	if _, evidenceErr := scenarioTestEvidenceMatches(ctx, tx, run, revision); evidenceErr != nil {
		if _, resetErr := tx.ExecContext(ctx, `UPDATE scenario_revisions SET status='draft',test_passed_at=NULL WHERE id=? AND status='testing'`, revision.ID); resetErr != nil {
			return resetErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return commitErr
		}
		return evidenceErr
	}
	result, err := tx.ExecContext(ctx, `UPDATE scenario_revisions SET status='test_passed',test_passed_at=? WHERE id=? AND status='testing' AND publication_generation=?`, timeText(at), revision.ID, revision.PublicationGeneration)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("%w: scenario revision changed before test evidence was recorded", domain.ErrConflict)
	}
	return publicationWriteError(tx.Commit())
}

// PublishCandidateReleaseSet commits the tested Scenario Revision and every
// opted-in Draft in one transaction. The successful test Run is re-read and
// checked against current Release digests before any status changes occur.
func (s *Store) PublishCandidateReleaseSet(ctx context.Context, revisionGuard ScenarioPublicationGuard, candidateReleaseIDs []string, releaseGuards []ReleasePublicationGuard, evidenceRunID string, expectedEpoch int64, at time.Time) error {
	tx, err := s.beginCatalogWrite(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	revision, err := getScenarioRevision(ctx, tx, revisionGuard.RevisionID)
	if err != nil {
		return err
	}
	if revision.Status != domain.RevisionTestPassed || revision.TestPassedAt == nil ||
		revision.PublicationGeneration != revisionGuard.PublicationGeneration ||
		domain.ScenarioRevisionSpecDigest(revision) != revisionGuard.SpecDigest {
		return fmt.Errorf("%w: scenario revision changed after publication validation", domain.ErrConflict)
	}
	evidence, err := getRunRecord(ctx, tx, evidenceRunID)
	if err != nil {
		return err
	}
	if revision.DigestVersion >= domain.ScenarioDigestVersion {
		if _, err := scenarioRequiredEvidenceTx(ctx, tx, revision); err != nil {
			return err
		}
	}
	evidenceReleases, err := scenarioTestEvidenceMatches(ctx, tx, evidence, revision)
	if err != nil {
		return err
	}
	guardedReleases, err := verifyReleasePublicationGuards(ctx, tx, releaseGuards)
	if err != nil {
		return err
	}
	candidates := make(map[string]struct{}, len(candidateReleaseIDs))
	for _, releaseID := range candidateReleaseIDs {
		if _, duplicate := candidates[releaseID]; duplicate {
			return fmt.Errorf("%w: duplicate candidate release %s", domain.ErrInvalid, releaseID)
		}
		candidates[releaseID] = struct{}{}
	}
	graphCandidates := map[string]domain.ComponentRelease{}
	for _, node := range revision.Graph.Nodes {
		release, ok := guardedReleases[node.ReleaseID]
		if !ok {
			return fmt.Errorf("%w: graph release %s is missing its publication guard", domain.ErrConflict, node.ReleaseID)
		}
		if _, tested := evidenceReleases[node.ReleaseID]; !tested {
			return fmt.Errorf("%w: graph release %s is missing current test evidence", domain.ErrConflict, node.ReleaseID)
		}
		if release.Status == domain.ReleaseDraft && release.Candidate && release.Review.Status == domain.ReleaseReviewApproved && release.Review.ContractDigest == domain.ComponentReleaseSpecDigest(release) {
			graphCandidates[release.ID] = release
		}
	}
	if len(candidates) != len(graphCandidates) {
		return fmt.Errorf("%w: candidate release set changed after validation", domain.ErrConflict)
	}
	for releaseID := range candidates {
		if _, ok := graphCandidates[releaseID]; !ok {
			return fmt.Errorf("%w: candidate release %s is no longer part of the tested graph", domain.ErrConflict, releaseID)
		}
	}
	if err := advancePublicationEpoch(ctx, tx, expectedEpoch); err != nil {
		return err
	}
	ordered := append([]string(nil), candidateReleaseIDs...)
	sort.Strings(ordered)
	for _, releaseID := range ordered {
		release := graphCandidates[releaseID]
		result, updateErr := tx.ExecContext(ctx, `UPDATE component_releases SET status='released',candidate=0,released_at=?,publication_generation=publication_generation+1 WHERE id=? AND status='draft' AND candidate=1 AND review_status='approved' AND review_contract_digest=? AND publication_generation=?`, timeText(at), releaseID, release.Review.ContractDigest, release.PublicationGeneration)
		if updateErr != nil {
			return publicationWriteError(updateErr)
		}
		if changed, _ := result.RowsAffected(); changed != 1 {
			return fmt.Errorf("%w: candidate release %s changed before atomic publish", domain.ErrConflict, releaseID)
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE scenario_revisions SET status='released',released_at=? WHERE id=? AND status='test_passed' AND test_passed_at IS NOT NULL AND publication_generation=?`, timeText(at), revision.ID, revision.PublicationGeneration)
	if err != nil {
		return publicationWriteError(err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("%w: scenario revision changed before atomic publish", domain.ErrConflict)
	}
	return publicationWriteError(tx.Commit())
}

func (s *Store) LatestSuccessfulScenarioTestRun(ctx context.Context, revisionID string) (domain.Run, error) {
	run, err := scanRun(s.db.QueryRowContext(ctx, runSelect+` WHERE kind='scenario_test' AND scenario_revision_id=? AND status='succeeded' ORDER BY COALESCE(finished_at,created_at) DESC,created_at DESC LIMIT 1`, revisionID))
	if errors.Is(err, sql.ErrNoRows) {
		return run, domain.ErrNotFound
	}
	return run, err
}
