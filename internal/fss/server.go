package fss

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Server struct {
	root    string
	allowed []*net.IPNet
}

type fileMetadata struct {
	RelativePath string `json:"relativePath"`
	Filename     string `json:"filename"`
	SHA256       string `json:"sha256"`
	SizeBytes    int64  `json:"sizeBytes"`
}

func New(root string, allowedCIDRs []string) (*Server, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	allowed := make([]*net.IPNet, 0, len(allowedCIDRs))
	for _, value := range allowedCIDRs {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		_, network, err := net.ParseCIDR(value)
		if err != nil {
			return nil, fmt.Errorf("invalid write allow CIDR %q: %w", value, err)
		}
		allowed = append(allowed, network)
	}
	if len(allowed) == 0 {
		return nil, errors.New("at least one write allow CIDR is required")
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &Server{root: abs, allowed: allowed}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	case r.Method == http.MethodPut && r.URL.Path == "/api/v1/files":
		if !s.writeAllowed(r) {
			http.Error(w, "write source is not allowed", http.StatusForbidden)
			return
		}
		s.upload(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/register":
		if !s.writeAllowed(r) {
			http.Error(w, "write source is not allowed", http.StatusForbidden)
			return
		}
		s.register(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/fetch":
		if !s.writeAllowed(r) {
			http.Error(w, "write source is not allowed", http.StatusForbidden)
			return
		}
		s.fetch(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/"):
		http.NotFound(w, r)
	default:
		s.serveFile(w, r)
	}
}

func (s *Server) writeAllowed(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, network := range s.allowed {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func safeRelativePath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, `\`, "/"))
	clean := filepath.ToSlash(filepath.Clean(value))
	if value == "" || clean == "." || strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path must be a safe relative path")
	}
	return clean, nil
}

func expectedSHA(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 64 {
		return "", errors.New("sha256 must contain 64 hexadecimal characters")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", errors.New("sha256 must contain 64 hexadecimal characters")
	}
	return value, nil
}

func (s *Server) resolve(relative string) (string, error) {
	clean, err := safeRelativePath(relative)
	if err != nil {
		return "", err
	}
	target := filepath.Join(s.root, filepath.FromSlash(clean))
	resolvedRoot, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return "", err
	}
	existing := filepath.Dir(target)
	var resolvedExisting string
	for {
		resolvedExisting, err = filepath.EvalSymlinks(existing)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) || existing == s.root {
			return "", err
		}
		existing = filepath.Dir(existing)
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedExisting)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes file station root")
	}
	return target, nil
}

func (s *Server) serveFile(w http.ResponseWriter, r *http.Request) {
	relative, err := safeRelativePath(strings.TrimPrefix(r.URL.Path, "/"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	target, err := s.resolve(relative)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, target)
}

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	relative, err := safeRelativePath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	expected, err := expectedSHA(r.URL.Query().Get("sha256"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target, err := s.resolve(relative)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".upload-*")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	temporaryName := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryName)
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(temporary, hash), r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		http.Error(w, "uploaded content SHA-256 does not match", http.StatusUnprocessableEntity)
		return
	}
	if err := temporary.Sync(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := temporary.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.Chmod(temporaryName, 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.Rename(temporaryName, target); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	committed = true
	writeJSON(w, http.StatusCreated, fileMetadata{RelativePath: relative, Filename: filepath.Base(target), SHA256: actual, SizeBytes: size})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&input); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	relative, err := safeRelativePath(input.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	expected, err := expectedSHA(input.SHA256)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	target, err := s.resolve(relative)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "registered path must be an existing regular file", http.StatusNotFound)
		return
	}
	file, err := os.Open(target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		http.Error(w, "registered content SHA-256 does not match", http.StatusUnprocessableEntity)
		return
	}
	writeJSON(w, http.StatusOK, fileMetadata{RelativePath: relative, Filename: filepath.Base(target), SHA256: actual, SizeBytes: info.Size()})
}

func (s *Server) fetch(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SourceURL string `json:"sourceUrl"`
		Path      string `json:"path"`
		SHA256    string `json:"sha256"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&input); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	parsed, err := url.Parse(strings.TrimSpace(input.SourceURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
		http.Error(w, "sourceUrl must be an HTTP or HTTPS URL", http.StatusBadRequest)
		return
	}
	relative, err := safeRelativePath(input.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	expected, err := expectedSHA(input.SHA256)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, parsed.String(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		http.Error(w, "fetch source failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		http.Error(w, "source returned "+response.Status, http.StatusBadGateway)
		return
	}
	s.storeFetched(w, relative, expected, response.Body)
}

func (s *Server) storeFetched(w http.ResponseWriter, relative, expected string, input io.Reader) {
	target, err := s.resolve(relative)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".fetch-*")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	temporaryName := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryName)
		}
	}()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(temporary, hash), input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		http.Error(w, "fetched content SHA-256 does not match", http.StatusUnprocessableEntity)
		return
	}
	if err := temporary.Sync(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := temporary.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.Chmod(temporaryName, 0o644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.Rename(temporaryName, target); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	committed = true
	writeJSON(w, http.StatusCreated, fileMetadata{RelativePath: relative, Filename: filepath.Base(target), SHA256: actual, SizeBytes: size})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
