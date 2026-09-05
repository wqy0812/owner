package service

import (
	"bytes"
	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/runarchive"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func (p *Platform) ConfigureRunArchives(root string) error {
	if root == "" {
		return nil
	}
	if !filepath.IsAbs(root) {
		return fmt.Errorf("归档目录必须是独立持久化目录的绝对路径")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if exe, e := os.Executable(); e == nil {
		parent := filepath.Dir(exe)
		if resolved == parent || strings.HasPrefix(resolved, parent+string(os.PathSeparator)) {
			return fmt.Errorf("归档目录不能位于程序目录内")
		}
	}
	p.archiveRoot = resolved
	return nil
}

// StartRunArchiveWorker is separate from execution scheduling. Defaults are off,
// and a missing archive directory never prevents normal Run execution.
func (p *Platform) StartRunArchiveWorker() {
	go func() {
		timer := time.NewTicker(3 * time.Second)
		defer timer.Stop()
		p.scanRunRetention(p.rootCtx)
		next := time.Now().Add(24 * time.Hour)
		for {
			select {
			case <-p.rootCtx.Done():
				return
			case <-timer.C:
				if time.Now().After(next) {
					p.scanRunRetention(p.rootCtx)
					next = time.Now().Add(24 * time.Hour)
				}
				if p.archiveRoot != "" && checkArchiveStorage(p.archiveRoot, 0) == nil {
					task, err := p.store.ClaimRunArchive(p.rootCtx, newID("lease"), time.Now().UTC())
					if err == nil {
						// archiveOne checks the capacity for this task. A large
						// failed task must not starve smaller queued archives.
						_ = p.processRunArchive(p.rootCtx, task)
					}
				}
			}
		}
	}()
}
func (p *Platform) processRunArchive(ctx context.Context, a domain.RunArchive) error {
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		timer := time.NewTicker(time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-done:
				return
			case <-workCtx.Done():
				return
			case <-timer.C:
				if err := p.store.RenewRunArchive(workCtx, a.RunID, a.LeaseToken, time.Now().UTC()); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	err := p.archiveOne(workCtx, a)
	if err != nil {
		_ = p.store.FailRunArchive(context.Background(), a.RunID, a.LeaseToken, Redact(err.Error()).(string), time.Now().UTC())
	}
	p.hub.Publish("run.updated", map[string]any{"runId": a.RunID})
	return err
}
func archiveSpaceSufficient(available uint64, needed int64) bool {
	return needed >= 0 && uint64(needed) <= (available-min(available, uint64(16<<20)))/2 && available >= 16<<20
}
func checkArchiveStorage(root string, needed int64) error {
	var disk syscall.Statfs_t
	if err := syscall.Statfs(root, &disk); err != nil {
		return fmt.Errorf("归档目录不可用，在线数据已保留: %w", err)
	}
	if !archiveSpaceSufficient(uint64(disk.Bavail)*uint64(disk.Bsize), needed) {
		return fmt.Errorf("归档目录空间不足，在线数据已保留")
	}
	probe, err := os.CreateTemp(root, ".storage-check-")
	if err != nil {
		return fmt.Errorf("归档目录不可写，在线数据已保留: %w", err)
	}
	name := probe.Name()
	err = probe.Close()
	if removeErr := os.Remove(name); err == nil {
		err = removeErr
	}
	return err
}
func (p *Platform) archiveOne(ctx context.Context, a domain.RunArchive) error {
	needed, err := p.store.RunArchiveBytes(ctx, a.RunID)
	if err != nil {
		return err
	}
	if err = checkArchiveStorage(p.archiveRoot, needed); err != nil {
		return err
	}
	pathHash := sha256.Sum256([]byte(a.RunID + ":" + a.LeaseToken))
	dir := filepath.Join(p.archiveRoot, ".packing-"+hex.EncodeToString(pathHash[:]))
	if err = os.Mkdir(dir, 0700); err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	logs, err := os.OpenFile(filepath.Join(dir, "logs.ndjson"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer logs.Close()
	boundary, err := p.store.ExportRunArchive(ctx, a.RunID, func(name string, raw []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var value any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			return err
		}
		clean, err := json.Marshal(Redact(value))
		if err != nil {
			return err
		}
		clean = append(clean, '\n')
		if name == "logs.ndjson" {
			_, err = logs.Write(clean)
			return err
		}
		return os.WriteFile(filepath.Join(dir, name), clean, 0600)
	})
	if err != nil {
		return err
	}
	if err = logs.Sync(); err != nil {
		return err
	}
	if err = logs.Close(); err != nil {
		return err
	}
	temp := filepath.Join(dir, "archive.tar.gz")
	if err = runarchive.Pack(ctx, dir, temp, a.RunID, boundary.LogCount); err != nil {
		return err
	}
	if err = runarchive.Verify(temp, a.RunID); err != nil {
		return err
	}
	hash, size, err := runarchive.HashFile(temp)
	if err != nil {
		return err
	}
	a.RelativePath = hex.EncodeToString(pathHash[:]) + ".tar.gz"
	a.SHA256 = hash
	a.SizeBytes = size
	a.FormatVersion = runarchive.FormatVersion
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(temp, filepath.Join(p.archiveRoot, a.RelativePath)); err != nil {
		return err
	}
	if err = runarchive.SyncDir(p.archiveRoot); err != nil {
		return err
	}
	// A failed final transaction intentionally leaves an orphan. Recovery never
	// uses existence of a file as permission to remove online logs.
	return p.store.CompleteRunArchive(ctx, a, boundary, time.Now().UTC())
}
func (s *ExecutionService) ArchiveRuns(ctx context.Context, user domain.User, ids []string) ([]domain.RunArchive, error) {
	if user.Role != domain.RolePlatformAdmin {
		return nil, domain.ErrForbidden
	}
	if s.platform.archiveRoot == "" {
		return nil, fmt.Errorf("%w: 尚未配置持久化归档目录", domain.ErrConflict)
	}
	if err := checkArchiveStorage(s.platform.archiveRoot, 0); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrConflict, err)
	}
	return s.platform.store.EnqueueRunArchives(ctx, ids, "manual", user.ID, time.Now().UTC())
}
func (s *ExecutionService) CleanupPreview(ctx context.Context, user domain.User, ids []string) ([]domain.RunCleanupPreview, error) {
	if user.Role != domain.RolePlatformAdmin {
		return nil, domain.ErrForbidden
	}
	return s.platform.store.PreviewRunCleanup(ctx, ids, time.Now().UTC())
}
func (s *ExecutionService) CleanupRuns(ctx context.Context, user domain.User, ids []string) error {
	if user.Role != domain.RolePlatformAdmin {
		return domain.ErrForbidden
	}
	if err := s.platform.store.CleanupRuns(ctx, ids, user.ID, "manual", time.Now().UTC()); err != nil {
		return err
	}
	for _, id := range ids {
		s.platform.hub.Publish("run.updated", map[string]any{"runId": id})
	}
	return nil
}
func (s *ExecutionService) ArchiveHealth(ctx context.Context, user domain.User) (domain.RunArchiveHealth, error) {
	if user.Role != domain.RolePlatformAdmin {
		return domain.RunArchiveHealth{}, domain.ErrForbidden
	}
	h, err := s.platform.store.RunArchiveHealth(ctx)
	h.Configured = s.platform.archiveRoot != ""
	if h.Configured {
		if storageErr := checkArchiveStorage(s.platform.archiveRoot, 0); storageErr != nil {
			h.StorageError = Redact(storageErr.Error()).(string)
		}
	}
	return h, err
}
func (s *ExecutionService) SaveRetention(ctx context.Context, user domain.User, policy domain.RunRetentionPolicy) error {
	if user.Role != domain.RolePlatformAdmin {
		return domain.ErrForbidden
	}
	if policy.AutoArchive && s.platform.archiveRoot == "" {
		return fmt.Errorf("%w: 请先配置持久化归档目录", domain.ErrConflict)
	}
	return s.platform.store.SaveRunRetentionPolicy(ctx, policy, user.ID, time.Now().UTC())
}
func (s *ExecutionService) ArchiveDownload(ctx context.Context, user domain.User, id string) (*os.File, domain.RunArchive, error) {
	var zero domain.RunArchive
	visible, err := s.store.CanViewRun(ctx, user, id)
	if err != nil {
		return nil, zero, err
	}
	if !visible {
		return nil, zero, domain.ErrForbidden
	}
	a, err := s.platform.store.GetRunArchive(ctx, id)
	if err != nil {
		return nil, a, err
	}
	if a.Status != "archived" {
		return nil, a, domain.ErrConflict
	}
	path, err := runarchive.Path(s.platform.archiveRoot, a.RelativePath)
	if err != nil {
		return nil, a, fmt.Errorf("%w: 归档文件丢失或不可读", domain.ErrConflict)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, a, fmt.Errorf("%w: 归档文件不可读", domain.ErrConflict)
	}
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil || hex.EncodeToString(h.Sum(nil)) != a.SHA256 || size != a.SizeBytes {
		f.Close()
		return nil, a, fmt.Errorf("%w: 归档文件损坏，下载不可用", domain.ErrConflict)
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		f.Close()
		return nil, a, err
	}
	return f, a, nil
}
func (s *ExecutionService) RunArchive(ctx context.Context, id string) (*domain.RunArchive, error) {
	a, err := s.platform.store.GetRunArchive(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, nil
	}
	return &a, err
}
func (s *ExecutionService) WasCleaned(ctx context.Context, id string) (bool, error) {
	return s.platform.store.RunWasCleaned(ctx, id)
}
func (p *Platform) scanRunRetention(ctx context.Context) {
	_ = p.reconcileArchiveOrphans(ctx)
	policy, err := p.store.RunRetentionPolicy(ctx)
	if err != nil || (!policy.AutoArchive && !policy.AutoCleanup) {
		return
	}
	now := time.Now().UTC()
	archived, cleaned, skipped, failed := 0, 0, 0, 0
	limit := 100
	if policy.AutoArchive && policy.AutoCleanup {
		limit = 50
	}
	record := func(id, action, reason string) {
		if e := p.store.AppendAudit(ctx, domain.AuditEvent{ID: newID("audit-retention"), ActorID: "system", Action: action, ResourceType: "run", ResourceID: id, Metadata: map[string]any{"reason": Redact(reason), "source": "automatic"}, CreatedAt: now}); e != nil {
			failed++
		}
	}
	for _, kind := range []string{"succeeded", "failed"} {
		if kind == "succeeded" && (!policy.AutoArchive || p.archiveRoot == "") {
			continue
		}
		if kind == "succeeded" && checkArchiveStorage(p.archiveRoot, 0) != nil {
			failed++
			continue
		}
		if kind == "failed" && !policy.AutoCleanup {
			continue
		}
		days := policy.CleanupDays
		if kind == "succeeded" {
			days = policy.ArchiveDays
		}
		after, afterID, _ := p.store.RetentionCursor(ctx, kind)
		ids, end, endID, e := p.store.RetentionCandidates(ctx, kind, now.Add(-time.Duration(days)*24*time.Hour), after, afterID, limit)
		if e != nil {
			failed++
			continue
		}
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			if kind == "succeeded" {
				if _, e = p.store.EnqueueRunArchives(ctx, []string{id}, "automatic", "system", now); e != nil {
					failed++
				} else {
					archived++
				}
			} else {
				preview, e := p.store.PreviewRunCleanup(ctx, []string{id}, now)
				if e != nil {
					failed++
					record(id, "run.cleanup_failed", e.Error())
				} else if !preview[0].Eligible {
					skipped++
					record(id, "run.cleanup_skipped", strings.Join(preview[0].Reasons, "；"))
				} else if e = p.store.CleanupRuns(ctx, []string{id}, "system", "automatic", now); e != nil {
					failed++
					record(id, "run.cleanup_failed", e.Error())
				} else {
					cleaned++
					p.hub.Publish("run.updated", map[string]any{"runId": id})
				}
			}
		}
		if len(ids) < limit {
			end, endID = "", ""
		}
		_ = p.store.SaveRetentionCursor(ctx, kind, end, endID)
	}
	_ = p.store.RecordRetentionScan(ctx, now, fmt.Sprintf("归档受理 %d，清理 %d，受保护跳过 %d，失败 %d", archived, cleaned, skipped, failed))
}

func (p *Platform) reconcileArchiveOrphans(ctx context.Context) error {
	if p.archiveRoot == "" {
		return nil
	}
	protected, err := p.store.ProtectedArchivePaths(ctx)
	if err != nil {
		return err
	}
	for key := range protected {
		parts := strings.Split(key, "\x00")
		if len(parts) == 2 {
			hash := sha256.Sum256([]byte(parts[1] + ":" + parts[0]))
			protected[hex.EncodeToString(hash[:])+".tar.gz"] = true
		}
	}
	entries, err := os.ReadDir(p.archiveRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".packing-") {
			key := strings.TrimPrefix(entry.Name(), ".packing-")
			if _, e := hex.DecodeString(key); e != nil || len(key) != 64 || protected[key+".tar.gz"] {
				continue
			}
			info, e := entry.Info()
			if e != nil {
				return e
			}
			if info.ModTime().Before(time.Now().Add(-24 * time.Hour)) {
				if e = os.RemoveAll(filepath.Join(p.archiveRoot, entry.Name())); e != nil {
					return e
				}
			}
			continue
		}
		if protected[entry.Name()] || entry.IsDir() || len(entry.Name()) != 71 || !strings.HasSuffix(entry.Name(), ".tar.gz") {
			continue
		}
		if _, err := hex.DecodeString(strings.TrimSuffix(entry.Name(), ".tar.gz")); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.ModTime().Before(time.Now().Add(-24*time.Hour)) && info.Mode().IsRegular() {
			if err = os.Remove(filepath.Join(p.archiveRoot, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
