package store

import (
	"context"
	"database/sql"
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestScenarioInstallationDigestTracksBackupMetadataAndRecoveryChain(t *testing.T) {
	installation := func() domain.EnvironmentComponentInstallation {
		return domain.EnvironmentComponentInstallation{
			NodeID: "node", EnvironmentID: "environment", ComponentID: "component", ReleaseID: "release-2",
			InstallRunID: "upgrade-run", BackupRef: "unchanged-backup-reference",
			Backup: domain.BackupMetadata{
				NodeID: "node", EnvironmentID: "environment", ComponentID: "component", ReleaseID: "release-2",
				InstallRunID: "upgrade-run", PlaybookSHA256: "current-playbook",
				DependencySnapshot: map[string]any{"upstreamReleaseId": "dependency-1"},
				Previous: &domain.EnvironmentComponentInstallation{
					NodeID: "node", EnvironmentID: "environment", ComponentID: "component", ReleaseID: "release-1",
					InstallRunID: "install-run", BackupRef: "original-install-backup",
					Backup: domain.BackupMetadata{InstallRunID: "install-run", PlaybookSHA256: "original-playbook"},
				},
			},
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*domain.EnvironmentComponentInstallation)
	}{
		{"current playbook identity", func(v *domain.EnvironmentComponentInstallation) { v.Backup.PlaybookSHA256 = "replaced-playbook" }},
		{"locked dependency", func(v *domain.EnvironmentComponentInstallation) {
			v.Backup.DependencySnapshot["upstreamReleaseId"] = "dependency-2"
		}},
		{"previous backup reference", func(v *domain.EnvironmentComponentInstallation) {
			v.Backup.Previous.BackupRef = "replaced-original-backup"
		}},
		{"previous backup metadata", func(v *domain.EnvironmentComponentInstallation) {
			v.Backup.Previous.Backup.PlaybookSHA256 = "replaced-original-playbook"
		}},
		{"removed recovery chain", func(v *domain.EnvironmentComponentInstallation) { v.Backup.Previous = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := installation(), installation()
			tc.mutate(&after)
			if before.BackupRef != after.BackupRef {
				t.Fatal("fixture must retain the current backup reference")
			}
			if ScenarioComponentInstallationDigest([]domain.EnvironmentComponentInstallation{before}) == ScenarioComponentInstallationDigest([]domain.EnvironmentComponentInstallation{after}) {
				t.Fatal("backup recovery identity changed without invalidating the installation digest")
			}
		})
	}
}

func TestScenarioInstallationTransactionDigestMatchesPublicRead(t *testing.T) {
	ctx := context.Background()
	db, _ := historicalBaselineFixture(t, false)
	readDigest := func() string {
		t.Helper()
		public, err := db.ListEnvironmentComponentInstallations(ctx, "env")
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.DB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		locked, err := scenarioInstallationsTx(ctx, tx, "env")
		if err != nil {
			t.Fatal(err)
		}
		if len(locked) != 1 || locked[0].Backup.PlaybookSHA256 == "" {
			t.Fatal("transaction omitted persisted backup metadata")
		}
		digest := ScenarioComponentInstallationDigest(public)
		if digest != ScenarioComponentInstallationDigest(locked) {
			t.Fatal("public preview and transaction validation disagree on the installation digest")
		}
		return digest
	}
	before := readDigest()
	if _, err := db.DB().Exec(`UPDATE environment_component_installations SET backup_metadata_json=json_set(backup_metadata_json,'$.playbookSha256','changed-after-preview') WHERE environment_id='env'`); err != nil {
		t.Fatal(err)
	}
	if after := readDigest(); after == before {
		t.Fatal("metadata drift with the same backup_ref did not invalidate the preview digest")
	}
}
