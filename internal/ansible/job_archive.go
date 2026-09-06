package ansible

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AddFile seals a platform-owned companion (the CLI or instructions) into the
// same manifest as the playbook. No caller-provided paths are accepted here.
func (b *JobBundle) AddFile(name string, data []byte, executable bool) error {
	if name != "clusterforge-job" && name != "README.md" {
		return fmt.Errorf("unsupported companion file")
	}
	mode := os.FileMode(0600)
	if executable {
		mode = 0700
	}
	if err := os.WriteFile(filepath.Join(b.Path, name), data, mode); err != nil {
		return err
	}
	digest, err := FileDigest(filepath.Join(b.Path, name))
	if err != nil {
		return err
	}
	b.Manifest.Files[name] = digest
	b.Manifest.Digest = ""
	b.Manifest.Digest = jsonDigest(b.Manifest)
	manifest, _ := json.MarshalIndent(b.Manifest, "", "  ")
	return writePrivateFile(filepath.Join(b.Path, "manifest.json"), manifest)
}

// ReadJobArchive accepts only the regular files used by sealed job bundles.
// It never extracts symlinks, absolute paths or parent-directory traversal.
func ReadJobArchive(input io.Reader) (*JobBundle, error) {
	root, err := os.MkdirTemp("", "cf-job-read-")
	if err != nil {
		return nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = os.RemoveAll(root)
		}
	}()
	gz, err := gzip.NewReader(input)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	reader := tar.NewReader(io.LimitReader(gz, 1<<30))
	seen := map[string]bool{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		name := header.Name
		if header.Typeflag != tar.TypeReg || filepath.IsAbs(name) || filepath.Clean(name) != name || strings.HasPrefix(name, "../") || name == ".." || strings.Contains(name, "\\") || seen[name] {
			return nil, fmt.Errorf("unsafe or duplicate job archive member")
		}
		seen[name] = true
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
		mode := os.FileMode(0600)
		if name == "clusterforge-job" {
			mode = 0700
		}
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return nil, err
		}
		_, copyErr := io.Copy(file, reader)
		closeErr := file.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	bundle, err := OpenJob(root)
	if err != nil {
		return nil, err
	}
	failed = false
	return bundle, nil
}

func (b *JobBundle) BindVerification(evidence JobVerification) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if !IsNativeJobContract(b.Manifest.Contract) || evidence.FullPlanDigest != b.Manifest.FullPlanDigest || evidence.RootBundleDigest != b.Manifest.Digest || len(evidence.RunIDs) == 0 || evidence.ScenarioRevisionID == "" {
		return fmt.Errorf("verification does not identify the full native job")
	}
	b.Manifest.Verification = &evidence
	b.Manifest.Digest = ""
	b.Manifest.Digest = jsonDigest(b.Manifest)
	data, err := json.MarshalIndent(b.Manifest, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(filepath.Join(b.Path, "manifest.json"), data)
}
func (b *JobBundle) WriteArchive(output io.Writer) error {
	if err := b.Validate(); err != nil {
		return err
	}
	gz := gzip.NewWriter(output)
	tw := tar.NewWriter(gz)
	names := []string{"manifest.json"}
	for name := range b.Manifest.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(b.Path, name)
		data, err := os.ReadFile(path)
		if err != nil {
			_ = tw.Close()
			_ = gz.Close()
			return err
		}
		mode := int64(0600)
		if name == "clusterforge-job" {
			mode = 0700
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: mode, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err := tw.Write(data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
