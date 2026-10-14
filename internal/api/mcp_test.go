package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"codex/platform-demo/internal/domain"
	"codex/platform-demo/internal/mcpcatalog"
	"codex/platform-demo/internal/mcpserver"
	"codex/platform-demo/internal/seed"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpRoundTripper struct {
	handler http.Handler
	token   string
}

func TestMCPScenarioExecutionPublicationAndPolling(t *testing.T) {
	f := newAPIFixture(t)
	template, err := f.database.GetEnvironmentRevision(context.Background(), "environment-test-r1")
	if err != nil {
		t.Fatal(err)
	}
	template.ID, template.EnvironmentID, template.Revision = "mcp-execution-r1", "mcp-execution", 1
	if err := f.database.CreateEnvironment(context.Background(), domain.Environment{ID: "mcp-execution", Name: "MCP execution", OwnerID: seed.EnvironmentOwnerID, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}, template); err != nil {
		t.Fatal(err)
	}
	_, tokens := setupMCP(t, f)
	owner := mcpClient(t, f.handler, tokens[seed.ScenarioOwnerID])
	created := mcpData(mcpCall(t, owner, "create_scenario", map[string]any{"body": map[string]any{"name": "MCP scenario", "slug": "mcp-scenario"}}, false))
	revisionID := created["currentRevisionId"].(string)
	path := map[string]string{"id": revisionID}
	definition := mcpData(mcpCall(t, owner, "read_scenario_acceptance", map[string]any{"path": path}, false))
	graph := map[string]any{"nodes": []any{map[string]any{"id": "runtime", "type": "component", "position": map[string]any{"x": 0, "y": 0}, "data": map[string]any{"label": "Runtime", "releaseId": "release-test-runtime-1.0.0", "action": "install", "parameterValues": map[string]any{}, "dependencySources": map[string]any{}}}}, "edges": []any{}}
	mcpCall(t, owner, "save_scenario_graph", map[string]any{"path": path, "body": map[string]any{"expectedDigest": definition["revisionDigest"], "graph": graph, "environmentConstraints": map[string]any{}}}, false)
	definition = mcpData(mcpCall(t, owner, "read_scenario_acceptance", map[string]any{"path": path}, false))
	definition = mcpData(mcpCall(t, owner, "save_scenario_acceptance", map[string]any{"path": path, "body": map[string]any{"expectedRevisionDigest": definition["revisionDigest"], "jobs": []any{map[string]any{"id": "business", "name": "Business check", "purpose": "MCP integration", "hostGroup": "test_nodes", "timeoutSeconds": 60, "riskLevel": "low"}}, "parameters": []any{}, "values": map[string]any{}, "bindings": []any{}}}, false))
	workspace := definition["workspace"].(map[string]any)
	mcpCall(t, owner, "save_scenario_acceptance_file", map[string]any{"path": path, "body": map[string]any{"path": "tasks/acceptance/business.yml", "content": "- ansible.builtin.assert:\n    that: true\n", "expectedRevisionDigest": definition["revisionDigest"], "expectedSha256": "", "expectedTreeSha256": workspace["treeSha256"]}}, false)
	input := map[string]any{"environmentId": "mcp-execution", "executionMode": "install", "testOnly": true, "idempotencyKey": "mcp-scenario-test"}
	var before int
	f.database.DB().QueryRow(`SELECT count(*) FROM runs`).Scan(&before)
	preview := mcpData(mcpCall(t, owner, "preview_scenario_execution", map[string]any{"path": path, "body": input}, false))
	var after int
	f.database.DB().QueryRow(`SELECT count(*) FROM runs`).Scan(&after)
	if before != after {
		t.Fatal("preview created a run")
	}
	if preview["ready"] != true {
		t.Fatalf("scenario not ready: %v", preview)
	}
	input["expectedPlanDigest"] = "stale"
	mcpCall(t, owner, "start_scenario_lifecycle_test", map[string]any{"path": path, "body": input}, true)
	input["expectedPlanDigest"] = preview["planDigest"]
	run := mcpData(mcpCall(t, owner, "start_scenario_lifecycle_test", map[string]any{"path": path, "body": input}, false))
	runID := run["id"].(string)
	repeated := mcpData(mcpCall(t, owner, "start_scenario_lifecycle_test", map[string]any{"path": path, "body": input}, false))
	if repeated["id"] != runID {
		t.Fatal("idempotency produced a second run")
	}
	waitForRun(t, f.database, runID, domain.RunSucceeded)
	got := mcpData(mcpCall(t, owner, "get_run", map[string]any{"path": map[string]string{"id": runID}}, false))
	if got["status"] != "succeeded" {
		t.Fatalf("run=%v", got)
	}
	mcpCall(t, owner, "run_activity", map[string]any{"path": map[string]string{"id": runID}, "query": map[string]string{"afterId": "0", "limit": "10"}}, false)
	mcpCall(t, owner, "publish_scenario", map[string]any{"path": path}, false)
	mcpCall(t, owner, "download_run_job", map[string]any{"path": map[string]string{"id": runID}}, false)
	envOwner := mcpClient(t, f.handler, tokens[seed.EnvironmentOwnerID])
	mcpCall(t, envOwner, "start_environment_rollback", map[string]any{"path": map[string]string{"id": "environment-test"}, "body": map[string]any{"expectedPlanDigest": "stale"}}, true)
	mcpCall(t, envOwner, "start_environment_rollback", map[string]any{"path": map[string]string{"id": "environment-test"}, "body": map[string]any{"expectedPlanDigest": "stale", "confirmEnvironmentName": "wrong environment"}}, true)
}

func TestMCPRealHTTPAndDisabledEndpoint(t *testing.T) {
	f := newAPIFixture(t)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, httptest.NewRequest("POST", "/mcp", nil))
	if w.Code != 404 {
		t.Fatal("MCP enabled by default")
	}
	_, tokens := setupMCP(t, f)
	server := httptest.NewServer(f.handler)
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "real-http-test", Version: "1"}, nil)
	httpClient := &http.Client{Transport: bearerHTTPTransport{token: tokens[seed.ComponentOwnerRuntimeID]}}
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: httpClient}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	mcpCall(t, session, "me", map[string]any{}, false)
}

type bearerHTTPTransport struct{ token string }

func (t bearerHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	r.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(r)
}

func (t mcpRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	r.Header.Set("Authorization", "Bearer "+t.token)
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, r)
	return w.Result(), nil
}

func setupMCP(t *testing.T, f *apiFixture) (string, map[string]string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	tokens := map[string]string{}
	config := mcpserver.Config{AllowedOrigins: []string{"https://ai.example.test"}, MaxFileBytes: 16 << 20, MaxTotalBytes: 64 << 20}
	for i, id := range []string{seed.ComponentOwnerRuntimeID, seed.ComponentOwnerK8sID, seed.ScenarioOwnerID, seed.EnvironmentOwnerID, seed.PlatformAdminID} {
		token := strings.Repeat(fmt.Sprint(i+1), 64)
		tokens[id] = token
		digest := sha256.Sum256([]byte(token))
		config.Credentials = append(config.Credentials, mcpserver.Credential{ClientID: fmt.Sprintf("client-%d", i), UserID: id, TokenSHA256: hex.EncodeToString(digest[:]), ExpiresAt: time.Now().Add(time.Hour)})
	}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := f.handler.(*Handler).EnableMCP(context.Background(), path, filepath.Join(dir, "transfers"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return path, tokens
}

func mcpClient(t *testing.T, h http.Handler, token string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "clusterforge-integration-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: "http://127.0.0.1/mcp", HTTPClient: &http.Client{Transport: mcpRoundTripper{h, token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func mcpCall(t *testing.T, c *mcp.ClientSession, name string, args any, wantError bool) map[string]any {
	t.Helper()
	res, err := c.CallTool(context.Background(), &mcp.CallToolParams{Name: "clusterforge_" + name, Arguments: args})
	if err != nil {
		t.Fatalf("%s protocol error: %v", name, err)
	}
	if res.IsError != wantError {
		t.Fatalf("%s isError=%v result=%#v", name, res.IsError, res.StructuredContent)
	}
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func mcpData(out map[string]any) map[string]any {
	return out["response"].(map[string]any)["data"].(map[string]any)
}

func TestMCPCatalogCoverageAndSchemas(t *testing.T) {
	generated, err := mcpcatalog.Generate("../..")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile("mcp_catalog_generated.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, saved) {
		t.Fatal("MCP catalog is stale; run go run ./cmd/mcp-catalog")
	}
	doc, err := mcpcatalog.Markdown(generated)
	if err != nil {
		t.Fatal(err)
	}
	savedDoc, err := os.ReadFile("../../docs/mcp-tools.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(doc, savedDoc) {
		t.Fatal("MCP tool documentation is stale; run go run ./cmd/mcp-catalog")
	}
	source, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatal(err)
	}
	routes := regexp.MustCompile(`HandleFunc\("([^"]+)"`).FindAllSubmatch(source, -1)
	h := &Handler{}
	ops := h.mcpOperations()
	byRoute := map[string]bool{}
	names := map[string]bool{}
	for _, op := range ops {
		if byRoute[op.Pattern] || names[op.Name] || len(op.Name) > 64 {
			t.Fatalf("duplicate or invalid operation %s", op.Name)
		}
		byRoute[op.Pattern] = true
		names[op.Name] = true
		checkRequiredProperties(t, op.InputSchema)
	}
	for _, route := range routes {
		p := string(route[1])
		_, excluded := mcpcatalog.Exclusions[p]
		if byRoute[p] == excluded {
			t.Fatalf("route %s must have exactly one mapping or exclusion", p)
		}
	}
	if len(ops) != len(routes)-len(mcpcatalog.Exclusions) {
		t.Fatalf("operations=%d routes=%d", len(ops), len(routes))
	}
	if len(ops) != 138 {
		t.Logf("review new business coverage count: %d", len(ops))
	}
}

func checkRequiredProperties(t *testing.T, s any) {
	t.Helper()
	switch v := s.(type) {
	case map[string]any:
		if required, ok := v["required"].([]string); ok {
			props, _ := v["properties"].(map[string]any)
			for _, name := range required {
				if _, ok := props[name]; !ok {
					t.Fatalf("required property %q has no schema", name)
				}
			}
		}
		for _, child := range v {
			checkRequiredProperties(t, child)
		}
	case []any:
		for _, child := range v {
			checkRequiredProperties(t, child)
		}
	}
}

func TestMCPDiscoveryIdentityPermissionsAndAudit(t *testing.T) {
	f := newAPIFixture(t)
	_, tokens := setupMCP(t, f)
	alice := mcpClient(t, f.handler, tokens[seed.ComponentOwnerRuntimeID])
	bob := mcpClient(t, f.handler, tokens[seed.ComponentOwnerK8sID])
	count := 0
	cursor := ""
	for {
		page, err := alice.ListTools(context.Background(), &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		count += len(page.Tools)
		for _, tool := range page.Tools {
			if strings.Contains(tool.Name, "switch_session") || strings.Contains(tool.Name, "session_users") {
				t.Fatal("login tool exposed")
			}
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if count != 143 {
		t.Fatalf("tool count=%d", count)
	}
	me := mcpData(mcpCall(t, alice, "me", map[string]any{}, false))
	if me["id"] != seed.ComponentOwnerRuntimeID {
		t.Fatalf("identity=%v", me)
	}
	created := mcpData(mcpCall(t, alice, "create_component", map[string]any{"body": map[string]any{"name": "MCP component", "slug": "mcp-component", "layer": "host_foundation"}}, false))
	id := created["id"].(string)
	denied := mcpCall(t, bob, "update_component", map[string]any{"path": map[string]string{"id": id}, "body": map[string]any{"name": "wrong owner", "slug": "mcp-component", "layer": "host_foundation"}}, true)
	if denied["status"] != float64(403) {
		t.Fatalf("cross-owner status=%v", denied)
	}
	mcpCall(t, alice, "me", map[string]any{"userId": seed.EnvironmentOwnerID}, true)
	for _, user := range []string{seed.ScenarioOwnerID, seed.EnvironmentOwnerID, seed.PlatformAdminID} {
		c := mcpClient(t, f.handler, tokens[user])
		out := mcpCall(t, c, "create_component", map[string]any{"body": map[string]any{"name": "denied", "slug": "denied", "layer": "host_foundation"}}, true)
		if out["status"] != float64(403) {
			t.Fatalf("role %s result=%v", user, out)
		}
	}
	admin := mcpClient(t, f.handler, tokens[seed.PlatformAdminID])
	mcpCall(t, admin, "create_platform_option_category", map[string]any{"body": map[string]any{"label": "MCP category"}}, false)
	listed := mcpCall(t, alice, "list_components", map[string]any{}, false)
	rest := decodeEnvelope(t, f.request("GET", "/api/v1/components", nil, f.session(seed.ComponentOwnerRuntimeID)))
	left, _ := json.Marshal(listed["response"])
	right, _ := json.Marshal(rest)
	if !bytes.Equal(left, right) {
		t.Fatal("REST and MCP component visibility differ")
	}
	var auditCount int
	if err := f.database.DB().QueryRow(`SELECT count(*) FROM audit_events WHERE action='mcp.tool_called' AND actor_id=?`, seed.ComponentOwnerRuntimeID).Scan(&auditCount); err != nil || auditCount < 3 {
		t.Fatalf("audit=%d err=%v", auditCount, err)
	}
}

func TestMCPComponentUpdatePreservesOmittedNameAndSlug(t *testing.T) {
	f := newAPIFixture(t)
	_, tokens := setupMCP(t, f)
	owner := mcpClient(t, f.handler, tokens[seed.ComponentOwnerRuntimeID])
	created := mcpData(mcpCall(t, owner, "create_component", map[string]any{"body": map[string]any{"name": "MCP partial update", "slug": "mcp-partial-update", "layer": "host_foundation"}}, false))
	path := map[string]string{"id": created["id"].(string)}
	patch := map[string]any{"description": "Updated through MCP", "layer": "runtime_state"}
	mcpCall(t, owner, "update_component", map[string]any{"path": path, "body": patch}, false)

	got := mcpData(mcpCall(t, owner, "get_component", map[string]any{"path": path}, false))
	if got["name"] != created["name"] || got["slug"] != created["slug"] {
		t.Fatalf("partial update changed omitted name or slug: %v", got)
	}
	if got["description"] != patch["description"] || got["layer"] != patch["layer"] {
		t.Fatalf("partial update was not persisted: %v", got)
	}
}

func TestMCPAuthReloadOriginAndNoCookieFallback(t *testing.T) {
	f := newAPIFixture(t)
	config, tokens := setupMCP(t, f)
	cookie := f.session(seed.ComponentOwnerRuntimeID)
	check := func(token, origin string, want int) {
		t.Helper()
		r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		r.AddCookie(cookie)
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body)
		}
	}
	check("", "", 401)
	check(strings.Repeat("x", 64), "", 401)
	check(tokens[seed.ComponentOwnerRuntimeID], "https://evil.test", 403)
	check(tokens[seed.ComponentOwnerRuntimeID], "https://ai.example.test", 200)
	client := mcpClient(t, f.handler, tokens[seed.ComponentOwnerRuntimeID])
	mcpCall(t, client, "me", map[string]any{}, false)
	data, _ := os.ReadFile(config)
	var cfg mcpserver.Config
	json.Unmarshal(data, &cfg)
	for i := range cfg.Credentials {
		cfg.Credentials[i].ExpiresAt = time.Now().Add(-time.Second)
	}
	data, _ = json.Marshal(cfg)
	os.WriteFile(config, data, 0600)
	check(tokens[seed.ComponentOwnerRuntimeID], "", 401)
	os.WriteFile(config, []byte(`{"credentials":[]}`), 0600)
	check(tokens[seed.ComponentOwnerRuntimeID], "", 401)
	os.WriteFile(config, []byte(`{broken`), 0600)
	check(tokens[seed.ComponentOwnerRuntimeID], "", 503)
}

func uploadMCP(t *testing.T, c *mcp.ClientSession, name string, contents []byte) string {
	t.Helper()
	hash := sha256.Sum256(contents)
	created := mcpCall(t, c, "transfer_create", map[string]any{"filename": name, "sizeBytes": len(contents), "sha256": hex.EncodeToString(hash[:])}, false)
	id := created["transferId"].(string)
	for offset := 0; offset < len(contents); offset += mcpserver.ChunkBytes {
		end := min(offset+mcpserver.ChunkBytes, len(contents))
		mcpCall(t, c, "transfer_write", map[string]any{"transferId": id, "offset": offset, "contentBase64": base64.StdEncoding.EncodeToString(contents[offset:end])}, false)
	}
	mcpCall(t, c, "transfer_finish", map[string]any{"transferId": id}, false)
	return id
}

func TestMCPFilesAndDraftPreviewDigest(t *testing.T) {
	f := newAPIFixture(t)
	_, tokens := setupMCP(t, f)
	alice := mcpClient(t, f.handler, tokens[seed.ComponentOwnerRuntimeID])
	bob := mcpClient(t, f.handler, tokens[seed.ComponentOwnerK8sID])
	created := mcpData(mcpCall(t, alice, "create_component", map[string]any{"body": map[string]any{"name": "MCP draft", "slug": "mcp-draft", "layer": "host_foundation"}}, false))
	id := created["id"].(string)
	input := map[string]any{"mode": "new_line", "lineName": "main", "version": "1.0.0", "releaseNotes": "MCP test release"}
	args := map[string]any{"path": map[string]string{"id": id}, "body": input}
	plan := mcpData(mcpCall(t, alice, "preview_release_draft", args, false))
	var drafts int
	f.database.DB().QueryRow(`SELECT count(*) FROM component_releases WHERE component_id=?`, id).Scan(&drafts)
	if drafts != 0 {
		t.Fatal("preview created a release")
	}
	input["expectedPlanDigest"] = "stale"
	mcpCall(t, alice, "create_release_draft", args, true)
	input["expectedPlanDigest"] = plan["planDigest"]
	release := mcpData(mcpCall(t, alice, "create_release_draft", args, false))
	releaseID := release["id"].(string)
	contents := []byte("- name: MCP test\n  hosts: all\n  tasks: []\n")
	transfer := uploadMCP(t, alice, "site.yml", contents)
	mcpCall(t, bob, "transfer_read", map[string]any{"transferId": transfer}, true)
	read := mcpCall(t, alice, "transfer_read", map[string]any{"transferId": transfer}, false)
	decoded, _ := base64.StdEncoding.DecodeString(read["contentBase64"].(string))
	if !bytes.Equal(decoded, contents) {
		t.Fatal("transfer bytes differ")
	}
	mcpCall(t, alice, "upload_release_playbook_workspace_file", map[string]any{"path": map[string]string{"id": releaseID}, "body": map[string]string{"path": "site.yml", "expectedSha256": ""}, "files": map[string]any{"file": map[string]string{"transferId": transfer}}}, false)
	out := mcpCall(t, alice, "get_release_playbook_workspace_file", map[string]any{"path": map[string]string{"id": releaseID}, "query": map[string]string{"path": "site.yml", "download": "1"}}, false)
	file := out["file"].(map[string]any)
	download := mcpCall(t, alice, "transfer_read", map[string]any{"transferId": file["transferId"]}, false)
	decoded, _ = base64.StdEncoding.DecodeString(download["contentBase64"].(string))
	if !bytes.Equal(decoded, contents) {
		t.Fatal("workspace download differs")
	}
	mcpCall(t, bob, "get_release_playbook_workspace_file", map[string]any{"path": map[string]string{"id": releaseID}, "query": map[string]string{"path": "site.yml", "download": "1"}}, true)
	mcpCall(t, alice, "delete_release_playbook_workspace_file", map[string]any{"path": map[string]string{"id": releaseID}, "query": map[string]string{"path": "site.yml", "expectedSha256": "stale"}}, true)
}
