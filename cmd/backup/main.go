package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"codex/platform-demo/internal/backup"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError()
	}
	baseConfig := configFromEnvironment()
	if args[0] == "automation-snapshot" {
		set := flag.NewFlagSet("automation-snapshot", flag.ContinueOnError)
		source := set.String("source", "", "automation source: before-deploy, after-v1-rebuild, or after-schema-migration")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if !validAutomationSource(*source) {
			return errors.New("--source must be before-deploy, after-v1-rebuild, or after-schema-migration")
		}
		config, found, err := backup.SelectedRepositoryConfig(baseConfig)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("no Catalog repository has been selected through the Environment Owner UI")
		}
		manager, err := backup.NewManager(config)
		if err != nil {
			return err
		}
		manifest, err := manager.Snapshot(ctx, *source)
		return printResult(manifest, err)
	}
	manager, err := backup.NewManager(baseConfig)
	if err != nil {
		return err
	}
	switch args[0] {
	case "resume":
		set := flag.NewFlagSet("resume", flag.ContinueOnError)
		backupID := set.String("backup-id", "", "partial backup identifier")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if *backupID == "" {
			return errors.New("--backup-id is required")
		}
		manifest, err := manager.Resume(ctx, *backupID)
		return printResult(manifest, err)
	case "list":
		items, err := manager.List()
		return printResult(items, err)
	case "verify":
		set := flag.NewFlagSet("verify", flag.ContinueOnError)
		backupID := set.String("backup-id", "", "backup identifier")
		verifyGit := set.Bool("verify-git", true, "verify the Git Catalog snapshot")
		verifyExternal := set.Bool("verify-external", false, "download FSS artifacts and inspect Registry digests")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if *backupID == "" {
			return errors.New("--backup-id is required")
		}
		manifest, err := manager.Verify(ctx, *backupID, *verifyGit)
		if err == nil && *verifyExternal {
			err = manager.VerifyExternal(ctx, *backupID)
		}
		return printResult(manifest, err)
	case "restore-plan":
		set := flag.NewFlagSet("restore-plan", flag.ContinueOnError)
		backupID := set.String("backup-id", "", "backup identifier")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if *backupID == "" {
			return errors.New("--backup-id is required")
		}
		plan, err := manager.RestorePlan(ctx, *backupID)
		return printResult(plan, err)
	case "restore-db":
		set := flag.NewFlagSet("restore-db", flag.ContinueOnError)
		backupID := set.String("backup-id", "", "backup identifier")
		target := set.String("target", "", "new SQLite database path")
		playbooks := set.String("target-playbook-root", "", "new Playbook root")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if *backupID == "" || *target == "" || *playbooks == "" {
			return errors.New("--backup-id, --target and --target-playbook-root are required")
		}
		plan, err := manager.RestoreDatabase(ctx, *backupID, *target, *playbooks)
		return printResult(plan, err)
	case "restore-catalog":
		set := flag.NewFlagSet("restore-catalog", flag.ContinueOnError)
		ref := set.String("tag", "", "backup tag or commit")
		target := set.String("target", "", "new SQLite database path")
		playbooks := set.String("target-playbook-root", "", "new Playbook root")
		if err := set.Parse(args[1:]); err != nil {
			return err
		}
		if *ref == "" || *target == "" || *playbooks == "" {
			return errors.New("--tag, --target and --target-playbook-root are required")
		}
		plan, err := manager.RestoreCatalog(ctx, *ref, *target, *playbooks)
		return printResult(plan, err)
	default:
		return usageError()
	}
}

func validAutomationSource(source string) bool {
	return source == "before-deploy" || source == "after-v1-rebuild" || source == "after-schema-migration"
}

func configFromEnvironment() backup.Config {
	playbookRoot := envOr("NEWPLATFORM_ALLOWED_ANSIBLE_ROOTS", "./examples/ansible")
	if first, _, found := strings.Cut(playbookRoot, ","); found {
		playbookRoot = first
	}
	return backup.Config{
		DatabasePath:  envOr("NEWPLATFORM_DB_PATH", "./data/newplatform.db"),
		PlaybookRoot:  strings.TrimSpace(playbookRoot),
		BackupDir:     envOr("CLUSTERFORGE_BACKUP_DIR", "./data/catalog-backups"),
		CatalogRepo:   envOr("CLUSTERFORGE_CATALOG_REPO", "./data/catalog-repo"),
		CatalogRemote: envOr("CLUSTERFORGE_CATALOG_REMOTE", "origin"),
		CatalogBranch: envOr("CLUSTERFORGE_CATALOG_BRANCH", "catalog"),
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func printResult(value any, err error) error {
	if value != nil {
		encoded, encodeErr := json.MarshalIndent(value, "", "  ")
		if encodeErr != nil {
			return encodeErr
		}
		fmt.Println(string(encoded))
	}
	return err
}

func usageError() error {
	return fmt.Errorf("usage: %s <automation-snapshot|resume|list|verify|restore-plan|restore-db|restore-catalog> [options]", filepath.Base(os.Args[0]))
}
