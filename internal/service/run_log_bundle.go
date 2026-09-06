package service

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/store"
)

type runLogContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r runLogContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func cleanRunLog(l domain.RunLog) domain.RunLog {
	// Event fields get the same recursive redaction as other API exports.
	var v any
	if l.Stream == "event" && json.Unmarshal([]byte(l.Message), &v) == nil {
		if event, ok := v.(map[string]any); ok {
			if result, ok := event["result"].(map[string]any); ok {
				if hidden, _ := result["_ansible_no_log"].(bool); hidden || result["censored"] != nil {
					event["task"] = "受保护任务"
					event["result"] = map[string]any{"_ansible_no_log": true, "censored": "输出已隐藏"}
				}
			}
		}
		data, _ := json.Marshal(Redact(v))
		l.Message = string(data)
	} else {
		l.Message = Redact(l.Message).(string)
	}
	return l
}
func (s *ExecutionService) readRunLogs(ctx context.Context, user domain.User, id string, visit func(domain.RunLog) error) (store.RunLogSnapshot, error) {
	var zero store.RunLogSnapshot
	visible, err := s.store.CanViewRun(ctx, user, id)
	if err != nil {
		return zero, err
	}
	if !visible {
		return zero, domain.ErrForbidden
	}
	snapshot, err := s.store.StreamRunLogSnapshot(ctx, id, func(l domain.RunLog) error { return visit(cleanRunLog(l)) })
	if err != nil || snapshot.Archive == nil {
		return snapshot, err
	}
	// The database snapshot decides the source. Never silently export an empty
	// DB log after an archive worker has atomically removed the original rows.
	f, _, err := s.ArchiveDownload(ctx, user, id)
	if err != nil {
		return snapshot, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(runLogContextReader{ctx, f})
	if err != nil {
		return snapshot, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return snapshot, e
		}
		if h.Name != "logs.ndjson" {
			continue
		}
		if found || h.Typeflag != tar.TypeReg {
			return snapshot, fmt.Errorf("%w: 无效的归档日志", domain.ErrConflict)
		}
		found = true
		dec := json.NewDecoder(tr)
		for {
			if e = ctx.Err(); e != nil {
				return snapshot, e
			}
			var l domain.RunLog
			e = dec.Decode(&l)
			if e == io.EOF {
				break
			}
			if e != nil {
				return snapshot, e
			}
			if l.RunID != id || l.ID <= snapshot.LastLogID {
				return snapshot, fmt.Errorf("%w: 归档日志标识或顺序无效", domain.ErrConflict)
			}
			if e = visit(cleanRunLog(l)); e != nil {
				return snapshot, e
			}
			snapshot.LastLogID = l.ID
			snapshot.LogCount++
		}
	}
	if !found {
		return snapshot, fmt.Errorf("%w: 归档包缺少完整日志", domain.ErrConflict)
	}
	return snapshot, nil
}

type RunLogBundle struct {
	File      *os.File
	Filename  string
	directory string
}

func (b *RunLogBundle) Close() { b.File.Close(); os.RemoveAll(b.directory) }

func (s *ExecutionService) RunLogBundle(ctx context.Context, user domain.User, id string) (_ *RunLogBundle, err error) {
	dir, err := os.MkdirTemp("", "clusterforge-run-logs-")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	logs, err := os.OpenFile(filepath.Join(dir, "logs.ndjson"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer logs.Close()
	plain, err := os.OpenFile(filepath.Join(dir, "logs.txt"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer plain.Close()
	var c diagnosticCollector
	enc := json.NewEncoder(logs)
	snapshot, err := s.readRunLogs(ctx, user, id, func(l domain.RunLog) error {
		c.add(l)
		if e := enc.Encode(l); e != nil {
			return e
		}
		_, e := fmt.Fprintf(plain, "#%d %s [%s] step=%s\n%s\n", l.ID, l.CreatedAt.Format("2006-01-02T15:04:05.000Z07:00"), l.Stream, l.StepID, l.Message)
		return e
	})
	if err != nil {
		return nil, err
	}
	if err = logs.Close(); err != nil {
		return nil, err
	}
	if err = plain.Close(); err != nil {
		return nil, err
	}
	diagnostics := RunDiagnostics{RunID: id, Status: snapshot.Run.Status, CapturedAt: snapshot.CapturedAt, LastLogID: snapshot.LastLogID, LogCount: snapshot.LogCount, Items: c.finish(snapshot.Run), Omitted: c.omitted}
	// Run summary deliberately excludes the executable input snapshot.
	run := snapshot.Run
	run.InputSnapshot = nil
	run.Steps = nil
	values := map[string]any{
		"manifest.json": map[string]any{"formatVersion": "clusterforge-run-logs-v1", "runId": id, "capturedAt": snapshot.CapturedAt, "lastLogId": snapshot.LastLogID, "logCount": snapshot.LogCount, "status": run.Status, "snapshot": run.FinishedAt == nil},
		"run.json":      run, "steps.json": snapshot.Run.Steps, "diagnostics.json": diagnostics,
	}
	for name, value := range values {
		encoded, e := json.Marshal(value)
		if e != nil {
			return nil, e
		}
		var plainValue any
		if e = json.Unmarshal(encoded, &plainValue); e != nil {
			return nil, e
		}
		data, e := json.MarshalIndent(Redact(plainValue), "", "  ")
		if e != nil {
			return nil, e
		}
		if e = os.WriteFile(filepath.Join(dir, name), data, 0600); e != nil {
			return nil, e
		}
	}
	target := filepath.Join(dir, "logs.tar.gz")
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			f.Close()
		}
	}()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"manifest.json", "run.json", "steps.json", "diagnostics.json", "logs.ndjson", "logs.txt"} {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		src, e := os.Open(filepath.Join(dir, name))
		if e != nil {
			return nil, e
		}
		info, e := src.Stat()
		if e != nil {
			src.Close()
			return nil, e
		}
		e = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: info.Size()})
		if e == nil {
			_, e = io.Copy(tw, runLogContextReader{ctx, src})
		}
		src.Close()
		if e != nil {
			return nil, e
		}
	}
	if err = tw.Close(); err != nil {
		return nil, err
	}
	if err = gz.Close(); err != nil {
		return nil, err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return &RunLogBundle{File: f, Filename: id + "-logs.tar.gz", directory: dir}, nil
}
