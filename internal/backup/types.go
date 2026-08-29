package backup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const CatalogFormatVersion = "clusterforge-catalog-v1"

type Config struct {
	DatabasePath  string
	PlaybookRoot  string
	BackupDir     string
	CatalogRepo   string
	CatalogRemote string
	CatalogBranch string
}

func (c Config) Validate() error {
	for name, value := range map[string]string{
		"database path": c.DatabasePath, "playbook root": c.PlaybookRoot,
		"backup directory": c.BackupDir, "catalog repository": c.CatalogRepo,
		"catalog remote": c.CatalogRemote, "catalog branch": c.CatalogBranch,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	if c.DatabasePath == ":memory:" || strings.HasPrefix(c.DatabasePath, "file:") {
		return fmt.Errorf("backup requires a filesystem SQLite database path")
	}
	if !safeGitName(c.CatalogRemote) {
		return fmt.Errorf("Catalog remote contains unsupported characters")
	}
	if !safeGitName(c.CatalogBranch) || strings.Contains(c.CatalogBranch, "..") || strings.Contains(c.CatalogBranch, "@{") {
		return fmt.Errorf("Catalog branch is not a safe Git ref")
	}
	return nil
}

func safeGitName(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasSuffix(value, ".") || strings.ContainsAny(value, " \\~^:?*[\t\r\n") {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("._/-", character)) {
			return false
		}
	}
	return true
}

func validateBackupID(value string) error {
	if value == "" || value == "." || value == ".." || filepath.Base(value) != value {
		return fmt.Errorf("backup ID is invalid")
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("._-", character)) {
			return fmt.Errorf("backup ID is invalid")
		}
	}
	return nil
}

type ManifestStatus string

const (
	StatusPartial ManifestStatus = "partial"
	StatusSuccess ManifestStatus = "success"
)

type Manifest struct {
	FormatVersion         string         `json:"formatVersion"`
	BackupID              string         `json:"backupId"`
	Status                ManifestStatus `json:"status"`
	Reason                string         `json:"reason"`
	CreatedAt             time.Time      `json:"createdAt"`
	CompletedAt           *time.Time     `json:"completedAt,omitempty"`
	DatabaseFile          string         `json:"databaseFile"`
	DatabaseSHA256        string         `json:"databaseSha256,omitempty"`
	SchemaContract        string         `json:"schemaContract,omitempty"`
	PublicationGeneration int64          `json:"publicationGeneration,omitempty"`
	CatalogSHA256         string         `json:"catalogSha256,omitempty"`
	GitCommit             string         `json:"gitCommit,omitempty"`
	GitTag                string         `json:"gitTag,omitempty"`
	Counts                map[string]int `json:"counts,omitempty"`
	Error                 string         `json:"error,omitempty"`
}

type Catalog struct {
	FormatVersion         string      `json:"formatVersion"`
	SchemaContract        string      `json:"schemaContract"`
	PublicationGeneration int64       `json:"publicationGeneration"`
	Tables                []TableDump `json:"tables"`
	Playbooks             []Playbook  `json:"playbooks"`
}

type TableDump struct {
	Name    string     `json:"name"`
	Columns []string   `json:"columns"`
	Rows    [][]DBCell `json:"rows"`
}

type DBCell struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
	Int  int64  `json:"int,omitempty"`
}

func (c DBCell) Value() any {
	switch c.Kind {
	case "null":
		return nil
	case "integer":
		return c.Int
	case "text":
		return c.Text
	default:
		return nil
	}
}

type Playbook struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"sizeBytes"`
}

type SnapshotMetadata struct {
	FormatVersion         string         `json:"formatVersion"`
	BackupID              string         `json:"backupId"`
	CreatedAt             time.Time      `json:"createdAt"`
	Reason                string         `json:"reason"`
	DatabaseSHA256        string         `json:"databaseSha256"`
	SchemaContract        string         `json:"schemaContract"`
	PublicationGeneration int64          `json:"publicationGeneration"`
	Counts                map[string]int `json:"counts"`
}

func canonicalJSON(value any) ([]byte, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func sha256Bytes(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}

func sha256File(path string) (string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha256Bytes(contents), nil
}

func writeJSONAtomic(path string, value any, mode os.FileMode) error {
	contents, err := canonicalJSON(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".json-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func catalogCounts(catalog Catalog) map[string]int {
	counts := make(map[string]int, len(catalog.Tables)+1)
	for _, table := range catalog.Tables {
		counts[table.Name] = len(table.Rows)
	}
	counts["playbooks"] = len(catalog.Playbooks)
	return counts
}

func sortCatalog(catalog *Catalog) {
	sort.Slice(catalog.Tables, func(i, j int) bool { return catalog.Tables[i].Name < catalog.Tables[j].Name })
	sort.Slice(catalog.Playbooks, func(i, j int) bool { return catalog.Playbooks[i].Path < catalog.Playbooks[j].Path })
}
