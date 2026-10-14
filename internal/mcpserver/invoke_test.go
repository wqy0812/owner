package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"codex/platform-demo/internal/domain"
)

func TestInvokeStreamsLargeMultipartAndPreservesLargeJSON(t *testing.T) {
	transfers := transferStore(t)
	s := &Server{transfers: transfers}
	ctx := context.WithValue(context.Background(), identityKey{}, identity{User: domain.User{ID: "user"}, Credential: Credential{ClientID: "client", TokenSHA256: "digest"}})
	owner := ownerKey(ctx)
	contents := bytes.Repeat([]byte("input"), 300000)
	f, err := transfers.Create(owner, "large.bin", "application/octet-stream", int64(len(contents)), sha(contents))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(contents); i += ChunkBytes {
		if _, err := transfers.Write(owner, f.TransferID, int64(i), contents[i:min(i+ChunkBytes, len(contents))]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := transfers.Finish(owner, f.TransferID); err != nil {
		t.Fatal(err)
	}
	called := false
	op := Operation{Pattern: "POST /api/v1/upload/{id}", Handle: func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.PathValue("id") != "target" {
			t.Error("path not bound")
		}
		if err := r.ParseMultipartForm(64 << 10); err != nil {
			t.Error(err)
			return
		}
		file, header, err := r.FormFile("artifact")
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		if !bytes.Equal(data, contents) || header.Filename != "large.bin" || r.FormValue("sha256") != sha(contents) {
			t.Error("multipart data changed")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": string(contents)})
	}}
	args, _ := json.Marshal(map[string]any{"path": map[string]string{"id": "target"}, "body": map[string]string{"sha256": sha(contents)}, "files": map[string]any{"artifact": map[string]string{"transferId": f.TransferID}}})
	res, err := s.invoke(ctx, op, args)
	if err != nil || res.IsError || !called {
		t.Fatalf("invoke: %v %#v", err, res)
	}
	out := res.StructuredContent.(map[string]any)
	info := out["file"].(FileInfo)
	file, _, release, err := transfers.Open(owner, info.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if sha(data) != info.SHA256 || int64(len(data)) != info.SizeBytes {
		t.Fatal("output metadata mismatch")
	}
	var decoded map[string]string
	if err := json.Unmarshal(data, &decoded); err != nil || decoded["data"] != string(contents) {
		t.Fatal("large JSON was truncated or changed")
	}
}

func TestInvokeQuotaRejectsBeforeMutation(t *testing.T) {
	transfers := transferStore(t)
	s := &Server{transfers: transfers}
	ctx := context.WithValue(context.Background(), identityKey{}, identity{User: domain.User{ID: "user"}, Credential: Credential{ClientID: "client"}})
	owner := ownerKey(ctx)
	for i := 0; i < 4; i++ {
		if _, err := transfers.Create(owner, "full", "", 2<<20, sha(nil)); err != nil {
			t.Fatal(err)
		}
	}
	called := false
	op := Operation{Pattern: "POST /api/v1/fixed", Handle: func(http.ResponseWriter, *http.Request) { called = true }}
	res, err := s.invoke(ctx, op, json.RawMessage(`{}`))
	if err != nil || !res.IsError || called {
		t.Fatalf("capacity gate invoked mutation: %v %v %v", res, err, called)
	}
}

func TestInvokeReleasesUploadWhenBusinessRejectsBeforeReading(t *testing.T) {
	transfers := transferStore(t)
	s := &Server{transfers: transfers}
	ctx := context.WithValue(context.Background(), identityKey{}, identity{User: domain.User{ID: "user"}, Credential: Credential{ClientID: "client"}})
	owner := ownerKey(ctx)
	data := []byte("uploaded content")
	f, err := transfers.Create(owner, "input.bin", "", int64(len(data)), sha(data))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transfers.Write(owner, f.TransferID, 0, data); err != nil {
		t.Fatal(err)
	}
	if _, err := transfers.Finish(owner, f.TransferID); err != nil {
		t.Fatal(err)
	}
	op := Operation{Pattern: "POST /api/v1/upload", Handle: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"error":"forbidden"}`)
	}}
	args, _ := json.Marshal(map[string]any{"files": map[string]any{"file": map[string]string{"transferId": f.TransferID}}})
	res, err := s.invoke(ctx, op, args)
	if err != nil || !res.IsError {
		t.Fatalf("early rejection: %v %v", res, err)
	}
	if err := transfers.Delete(owner, f.TransferID); err != nil {
		t.Fatalf("early rejection left an upload pinned: %v", err)
	}
}
