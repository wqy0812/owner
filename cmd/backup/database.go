package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"codex/platform-demo/internal/backup"
	"codex/platform-demo/internal/deploydb"
)

func runDatabase(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("database requires contract, active-runs, active-work, snapshot, business-snapshot, foundation-snapshot, business-export, verify, verify-business, verify-foundation, history-snapshot, history-verify, or history-restore")
	}
	set := flag.NewFlagSet("database "+args[0], flag.ContinueOnError)
	archiveRoot := set.String("archive-dir", "", "persistent Run archive root")
	path := set.String("db", "", "existing SQLite database path")
	target := set.String("target", "", "new snapshot destination")
	expected := set.String("expected-contract", "", "exact required schema contract")
	if err := set.Parse(args[1:]); err != nil {
		return err
	}
	if *path == "" || set.NArg() != 0 {
		return errors.New("--db is required; positional arguments are not accepted")
	}
	switch args[0] {
	case "history-snapshot":
		return backup.SnapshotRunHistory(ctx, *path, *archiveRoot, *target)
	case "history-verify":
		return backup.VerifyRunHistory(ctx, *path)
	case "history-restore":
		return backup.RestoreRunHistory(ctx, *path, *target)

	case "verify-business", "verify-foundation":
		return deploydb.VerifyBusiness(ctx, *path, args[0] == "verify-foundation")
	case "active-work":
		work, err := deploydb.ActiveWork(ctx, *path)
		if err == nil && len(work) > 0 {
			fmt.Println(strings.Join(work, "\n"))
		}
		return err
	case "contract":
		contract, err := deploydb.Contract(ctx, *path)
		if err == nil {
			fmt.Println(contract)
		}
		return err
	case "active-runs":
		runs, err := deploydb.ActiveRuns(ctx, *path)
		if err == nil && len(runs) > 0 {
			fmt.Println(strings.Join(runs, "\n"))
		}
		return err
	case "business-snapshot", "foundation-snapshot":
		if *target == "" {
			return errors.New("--target is required")
		}
		return deploydb.BusinessSnapshot(ctx, *path, *target, args[0] == "foundation-snapshot")
	case "business-export":
		if *target == "" {
			return errors.New("--target is required")
		}
		return deploydb.ExportBusiness(ctx, *path, *target)
	case "snapshot":
		if *target == "" {
			return errors.New("--target is required")
		}
		return deploydb.Snapshot(ctx, *path, *target)
	case "verify":
		if *expected == "" {
			return errors.New("--expected-contract is required")
		}
		return deploydb.Verify(ctx, *path, *expected)
	default:
		return fmt.Errorf("unknown database command %q", args[0])
	}
}
