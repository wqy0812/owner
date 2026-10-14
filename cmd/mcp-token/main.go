// mcp-token creates local credential files without printing bearer secrets.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"codex/platform-demo/internal/mcpserver"
)

func main() {
	dir := flag.String("out", "", "new private output directory (must not exist)")
	client := flag.String("client-id", "", "unique MCP client identifier")
	user := flag.String("user-id", "", "existing platform user ID")
	ttl := flag.Duration("ttl", 90*24*time.Hour, "credential lifetime")
	flag.Parse()
	if err := generate(*dir, *client, *user, *ttl); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Created %s and %s. Bearer secret was not printed.\n", filepath.Join(*dir, "config.json"), filepath.Join(*dir, "token"))
}

func generate(dir, client, user string, ttl time.Duration) error {
	if dir == "" || client == "" || len(client) > 128 || user == "" || ttl <= 0 {
		return errors.New("out, client-id, user-id and a positive ttl are required")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(b[:])
	hash := sha256.Sum256([]byte(token))
	c := mcpserver.Config{Credentials: []mcpserver.Credential{{ClientID: client, UserID: user, TokenSHA256: hex.EncodeToString(hash[:]), ExpiresAt: time.Now().UTC().Add(ttl)}}, AllowedOrigins: []string{}}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), append(data, '\n'), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte(token+"\n"), 0600); err != nil {
		return err
	}
	ok = true
	return nil
}
