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
