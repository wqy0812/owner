// Package runarchive handles immutable, independently verifiable archive files.
package runarchive

import (
	"archive/tar"
	"compress/gzip"
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
)

const FormatVersion = "clusterforge-run-archive-v1"

type Entry struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	FormatVersion string  `json:"formatVersion"`
	RunID         string  `json:"runId"`
	LogCount      int64   `json:"logCount"`
	Files         []Entry `json:"files"`
}

func HashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), n, err
}
func SyncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func Path(root, relative string) (string, error) {
	if !filepath.IsAbs(root) || filepath.Base(relative) != relative || !strings.HasSuffix(relative, ".tar.gz") {
		return "", errors.New("invalid archive path")
	}
	path := filepath.Join(root, relative)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("archive is not a regular file")
	}
	return path, nil
}
func Pack(ctx context.Context, dir, target, runID string, count int64) (err error) {
	manifest := Manifest{FormatVersion: FormatVersion, RunID: runID, LogCount: count, Files: []Entry{}}
	for _, name := range []string{"run.json", "run_steps.json", "approvals.json", "logs.ndjson"} {
		hash, size, e := HashFile(filepath.Join(dir, name))
		if e != nil {
			return e
		}
		manifest.Files = append(manifest.Files, Entry{Name: name, Size: size, SHA256: hash})
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(target)
		}
	}()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	if err = tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(data))}); err != nil {
		return err
	}
	if _, err = tw.Write(data); err != nil {
		return err
	}
	for _, entry := range manifest.Files {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = tw.WriteHeader(&tar.Header{Name: entry.Name, Mode: 0600, Size: entry.Size}); err != nil {
			return err
		}
		src, e := os.Open(filepath.Join(dir, entry.Name))
		if e != nil {
			return e
		}
		_, e = io.Copy(tw, src)
		src.Close()
		if e != nil {
			return e
		}
	}
	if err = tw.Close(); err != nil {
		return err
	}
	if err = gz.Close(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	return f.Close()
}
func Verify(path, expectedRunID string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	header, err := tr.Next()
	if err != nil {
		return err
	}
	if header.Name != "manifest.json" || header.Size > 1<<20 {
		return errors.New("invalid archive manifest")
	}
	var m Manifest
	if err = json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&m); err != nil {
		return err
	}
	if m.FormatVersion != FormatVersion || m.RunID != expectedRunID || len(m.Files) != 4 {
		return errors.New("archive identity or file list mismatch")
	}
	expected := map[string]Entry{}
	for _, e := range m.Files {
		if e.Name != "run.json" && e.Name != "run_steps.json" && e.Name != "approvals.json" && e.Name != "logs.ndjson" {
			return errors.New("unexpected archive entry")
		}
		if _, exists := expected[e.Name]; exists {
			return errors.New("duplicate archive entry")
		}
		expected[e.Name] = e
	}
	for {
		entry, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		want, ok := expected[entry.Name]
		if !ok || entry.Typeflag != tar.TypeReg || entry.Size != want.Size {
			return errors.New("archive file list mismatch")
		}
		h := sha256.New()
		var n int64
		if entry.Name == "logs.ndjson" {
			dec := json.NewDecoder(io.TeeReader(tr, h))
			var last int64
			for {
				var row struct {
					ID    int64  `json:"id"`
					RunID string `json:"runId"`
				}
				e = dec.Decode(&row)
				if e == io.EOF {
					break
				}
				if e != nil {
					return e
				}
				if row.ID <= last || row.RunID != m.RunID {
					return errors.New("archive log identity or order mismatch")
				}
				last = row.ID
				n++
			}
			if n != m.LogCount {
				return errors.New("archive log count mismatch")
			}
		} else {
			if _, e = io.Copy(h, tr); e != nil {
				return e
			}
		}
		if hex.EncodeToString(h.Sum(nil)) != want.SHA256 {
			return fmt.Errorf("archive checksum mismatch: %s", entry.Name)
		}
		delete(expected, entry.Name)
	}
	if len(expected) != 0 {
		return errors.New("archive files missing")
	}
	_, err = io.Copy(io.Discard, gz)
	return err
}
