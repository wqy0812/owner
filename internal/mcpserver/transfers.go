package mcpserver

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const ChunkBytes = 1 << 20
const transferTTL = time.Hour
const maxTransfers = 1024

type FileInfo struct {
	TransferID string    `json:"transferId"`
	Filename   string    `json:"filename"`
	MediaType  string    `json:"mediaType"`
	SizeBytes  int64     `json:"sizeBytes"`
	SHA256     string    `json:"sha256"`
	ExpiresAt  time.Time `json:"expiresAt"`
	Complete   bool      `json:"complete"`
}

type transfer struct {
	FileInfo
	owner   string
	path    string
	written int64
	readers int
}

type Transfers struct {
	mu                sync.Mutex
	dir               string
	lock              *os.File
	files             map[string]*transfer
	reserved          int64
	maxFile, maxTotal int64
	stop              chan struct{}
	done              chan struct{}
	once              sync.Once
}

func (s *Transfers) SetLimits(maxFile, maxTotal int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxFile, s.maxTotal = maxFile, maxTotal
}

func NewTransfers(dir string, maxFile, maxTotal int64) (*Transfers, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("MCP transfer directory must be a private directory (0700)")
	}
	lock, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("MCP transfer directory is already in use")
	}
	s := &Transfers{dir: dir, lock: lock, files: map[string]*transfer{}, maxFile: maxFile, maxTotal: maxTotal, stop: make(chan struct{}), done: make(chan struct{})}
	// Only our exact generated filenames are removed. The exclusive lock prevents
	// cleanup from touching a running server's files after a crash/restart.
	entries, err := os.ReadDir(dir)
	if err != nil {
		lock.Close()
		return nil, err
	}
	for _, entry := range entries {
		if validTransferID(entry.Name()) {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				lock.Close()
				return nil, err
			}
		}
	}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case now := <-ticker.C:
				s.sweep(now)
			}
		}
	}()
	return s, nil
}

func randomID() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "mcp-transfer-" + hex.EncodeToString(b[:])
}
func validTransferID(id string) bool {
	if !strings.HasPrefix(id, "mcp-transfer-") || len(id) != 61 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(id, "mcp-transfer-"))
	return err == nil
}

func (s *Transfers) Create(owner, filename, mediaType string, size int64, digest string) (FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if filepath.Base(filename) != filename || filename == "" || filename == "." || strings.ContainsAny(filename, "\\\r\n\x00") {
		return FileInfo{}, errors.New("filename must be a simple filename")
	}
	b, err := hex.DecodeString(digest)
	if err != nil || len(b) != 32 || digest != strings.ToLower(digest) {
		return FileInfo{}, errors.New("sha256 must be a lowercase SHA-256 digest")
	}
	return s.createLocked(owner, filename, mediaType, size, digest)
}

func (s *Transfers) createLocked(owner, filename, mediaType string, size int64, digest string) (FileInfo, error) {
	if size < 0 || size > s.maxFile || size > s.maxTotal-s.reserved || len(s.files) >= maxTransfers {
		return FileInfo{}, errors.New("MCP transfer quota exceeded")
	}
	id := randomID()
	path := filepath.Join(s.dir, id)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return FileInfo{}, err
	}
	f.Close()
	v := &transfer{FileInfo: FileInfo{TransferID: id, Filename: filename, MediaType: mediaType, SizeBytes: size, SHA256: digest, ExpiresAt: time.Now().UTC().Add(transferTTL)}, owner: owner, path: path}
	s.files[id] = v
	s.reserved += size
	return v.FileInfo, nil
}

func (s *Transfers) lookup(owner, id string) (*transfer, error) {
	v := s.files[id]
	if v == nil || v.owner != owner || !time.Now().Before(v.ExpiresAt) {
		return nil, errors.New("transfer not found or expired")
	}
	return v, nil
}

func (s *Transfers) Write(owner, id string, offset int64, data []byte) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.lookup(owner, id)
	if err != nil {
		return 0, err
	}
	if v.Complete || offset < 0 || len(data) == 0 || len(data) > ChunkBytes || offset > v.written || int64(len(data)) > v.SizeBytes-offset {
		return 0, errors.New("invalid chunk, offset, or completed transfer")
	}
	f, err := os.OpenFile(v.path, os.O_RDWR, 0600)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if offset < v.written {
		if offset+int64(len(data)) > v.written {
			return 0, errors.New("overlapping chunk")
		}
		previous := make([]byte, len(data))
		if _, err = f.ReadAt(previous, offset); err != nil {
			return 0, err
		}
		if !bytes.Equal(data, previous) {
			return 0, errors.New("repeated chunk differs from stored data")
		}
		return v.written, nil
	}
	n, err := f.WriteAt(data, offset)
	v.written += int64(n)
	return v.written, err
}

func (s *Transfers) Finish(owner, id string) (FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.lookup(owner, id)
	if err != nil {
		return FileInfo{}, err
	}
	if v.Complete {
		return v.FileInfo, nil
	}
	if v.written != v.SizeBytes {
		return FileInfo{}, errors.New("upload is incomplete")
	}
	f, err := os.Open(v.path)
	if err != nil {
		return FileInfo{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return FileInfo{}, err
	}
	if hex.EncodeToString(h.Sum(nil)) != v.SHA256 {
		return FileInfo{}, errors.New("upload SHA-256 mismatch; delete and upload again")
	}
	v.Complete = true
	return v.FileInfo, nil
}

// Open pins an immutable completed file until release, so cleanup cannot unlink
// input while the platform is streaming it to its existing upload handler.
func (s *Transfers) Open(owner, id string) (*os.File, FileInfo, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.lookup(owner, id)
	if err != nil {
		return nil, FileInfo{}, nil, err
	}
	if !v.Complete {
		return nil, FileInfo{}, nil, errors.New("transfer is not complete")
	}
	f, err := os.Open(v.path)
	if err != nil {
		return nil, FileInfo{}, nil, err
	}
	v.readers++
	var once sync.Once
	release := func() { once.Do(func() { f.Close(); s.mu.Lock(); v.readers--; s.mu.Unlock() }) }
	return f, v.FileInfo, release, nil
}

func (s *Transfers) Delete(owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.lookup(owner, id)
	if err != nil {
		return err
	}
	if v.readers > 0 {
		return errors.New("transfer is in use")
	}
	return s.remove(v)
}
func (s *Transfers) remove(v *transfer) error {
	if err := os.Remove(v.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(s.files, v.TransferID)
	s.reserved -= v.SizeBytes
	return nil
}
func (s *Transfers) sweep(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range s.files {
		if !now.Before(v.ExpiresAt) && v.readers == 0 {
			_ = s.remove(v)
		}
	}
}
func (s *Transfers) Close() error {
	var err error
	s.once.Do(func() {
		close(s.stop)
		<-s.done
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, v := range s.files {
			if e := s.remove(v); e != nil {
				err = e
			}
		}
		if e := s.lock.Close(); e != nil {
			err = e
		}
	})
	return err
}

// Output reserves its entire bounded allowance before business execution. This
// prevents an already-committed operation from losing its result to quota races.
func (s *Transfers) Output(owner string) (*os.File, FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info, err := s.createLocked(owner, "result.bin", "application/octet-stream", s.maxFile, "")
	if err != nil {
		return nil, FileInfo{}, err
	}
	f, err := os.OpenFile(s.files[info.TransferID].path, os.O_WRONLY, 0600)
	if err != nil {
		_ = s.remove(s.files[info.TransferID])
		return nil, FileInfo{}, err
	}
	return f, info, nil
}

func (s *Transfers) SealOutput(owner, id, filename, mediaType string, size int64) (FileInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.lookup(owner, id)
	if err != nil {
		return FileInfo{}, err
	}
	if size < 0 || size > v.SizeBytes {
		return FileInfo{}, errors.New("result exceeded transfer limit")
	}
	f, err := os.Open(v.path)
	if err != nil {
		return FileInfo{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return FileInfo{}, err
	}
	if n != size {
		return FileInfo{}, fmt.Errorf("result length mismatch")
	}
	s.reserved -= v.SizeBytes - size
	v.SizeBytes = size
	v.written = size
	v.SHA256 = hex.EncodeToString(h.Sum(nil))
	v.Filename = filepath.Base(filename)
	v.MediaType = mediaType
	v.Complete = true
	return v.FileInfo, nil
}
