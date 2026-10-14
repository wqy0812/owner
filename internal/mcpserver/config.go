// Package mcpserver adapts a fixed set of platform operations to MCP. It never
// accepts a destination URL, an impersonated user, or a server filesystem path.
package mcpserver

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"
)

type Credential struct {
	ClientID    string    `json:"clientId"`
	UserID      string    `json:"userId"`
	TokenSHA256 string    `json:"tokenSha256"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type Config struct {
	Credentials    []Credential `json:"credentials"`
	AllowedOrigins []string     `json:"allowedOrigins"`
	MaxFileBytes   int64        `json:"maxFileBytes,omitempty"`
	MaxTotalBytes  int64        `json:"maxTotalBytes,omitempty"`
}

// ReadConfig is intentionally called on every request: revocation does not
// require stopping the executor, and a broken config never falls back to old keys.
func ReadConfig(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, fmt.Errorf("open MCP configuration: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return c, errors.New("MCP configuration exceeds 64 KiB or cannot be read")
	}
	if err := strictJSON(data, &c); err != nil {
		return c, errors.New("invalid MCP configuration JSON")
	}
	clients, hashes := map[string]bool{}, map[string]bool{}
	for _, key := range c.Credentials {
		digest, err := hex.DecodeString(key.TokenSHA256)
		if key.ClientID == "" || len(key.ClientID) > 128 || key.UserID == "" || key.ExpiresAt.IsZero() || err != nil || len(digest) != sha256.Size || key.TokenSHA256 != strings.ToLower(key.TokenSHA256) || clients[key.ClientID] || hashes[key.TokenSHA256] {
			return c, errors.New("invalid or duplicate MCP credential; clientId, userId, lowercase tokenSha256 and expiresAt are required")
		}
		clients[key.ClientID], hashes[key.TokenSHA256] = true, true
	}
	for _, origin := range c.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return c, errors.New("MCP allowedOrigins must contain exact http(s) origins without paths")
		}
	}
	if c.MaxFileBytes == 0 {
		c.MaxFileBytes = 2 << 30
	}
	if c.MaxTotalBytes == 0 {
		c.MaxTotalBytes = 8 << 30
	}
	if c.MaxFileBytes < ChunkBytes || c.MaxTotalBytes < c.MaxFileBytes {
		return c, errors.New("invalid MCP transfer quotas")
	}
	return c, nil
}

func (c Config) credential(token string, now time.Time) (Credential, bool) {
	if len(token) < 32 || len(token) > 256 {
		return Credential{}, false
	}
	digest := sha256.Sum256([]byte(token))
	for _, key := range c.Credentials {
		expected, _ := hex.DecodeString(key.TokenSHA256)
		if subtle.ConstantTimeCompare(digest[:], expected) == 1 && now.Before(key.ExpiresAt) {
			return key, true
		}
	}
	return Credential{}, false
}

func strictJSON(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}
