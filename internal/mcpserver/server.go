package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"codex/platform-demo/internal/domain"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Operation struct {
	Name        string
	Pattern     string
	Description string
	InputSchema Schema
	ReadOnly    bool
	Idempotent  bool
	Handle      http.HandlerFunc
}

type identity struct {
	User       domain.User
	Credential Credential
}
type identityKey struct{}

func CurrentUser(ctx context.Context) domain.User {
	if v, ok := ctx.Value(identityKey{}).(identity); ok {
		return v.User
	}
	return domain.User{}
}
func ownerKey(ctx context.Context) string {
	v := ctx.Value(identityKey{}).(identity)
	return v.Credential.ClientID + "\x00" + v.User.ID + "\x00" + v.Credential.TokenSHA256
}

type Audit struct {
	ClientID, UserID, ToolName, RequestID string
	Status                                int
	Duration                              time.Duration
}
type Options struct {
	ConfigPath  string
	TransferDir string
	LookupUser  func(context.Context, string) (domain.User, error)
	Audit       func(context.Context, Audit)
}
type Server struct {
	options   Options
	transfers *Transfers
	handler   http.Handler
	active    chan struct{}
}

func New(ctx context.Context, options Options, operations []Operation) (*Server, error) {
	c, err := ReadConfig(options.ConfigPath)
	if err != nil {
		return nil, err
	}
	for _, key := range c.Credentials {
		if _, err := options.LookupUser(ctx, key.UserID); err != nil {
			return nil, fmt.Errorf("MCP credential %q references an unavailable user", key.ClientID)
		}
	}
	transfers, err := NewTransfers(options.TransferDir, c.MaxFileBytes, c.MaxTotalBytes)
	if err != nil {
		return nil, err
	}
	s := &Server{options: options, transfers: transfers, active: make(chan struct{}, 4)}
	server := mcp.NewServer(&mcp.Implementation{Name: "clusterforge", Version: "1.0.0"}, &mcp.ServerOptions{
		PageSize:     50,
		Instructions: "ClusterForge tools act as the user bound to your Bearer token. Use clusterforge_me to identify that user. Follow preview -> submit with the returned expectedPlanDigest and current revision/workspace digests. Never invent confirmation values or retry mutating operations automatically after a timeout; inspect runs first. Long operations return IDs: poll run activity with afterId. Large results return transferId: use clusterforge_transfer_read. Upload files using transfer_create/write/finish, then pass files.<field>.transferId. No identity switching is available.",
	})
	seen := map[string]bool{}
	for _, operation := range operations {
		op := operation
		if seen[op.Name] {
			transfers.Close()
			return nil, fmt.Errorf("duplicate MCP operation %s", op.Name)
		}
		seen[op.Name] = true
		if err := s.addTool(server, op.Name, op.Description, op.InputSchema, op.ReadOnly, op.Idempotent, func(ctx context.Context, raw json.RawMessage) (*mcp.CallToolResult, error) {
			return s.invoke(ctx, op, raw)
		}); err != nil {
			transfers.Close()
			return nil, err
		}
	}
	if err := s.addTransferTools(server); err != nil {
		transfers.Close()
		return nil, err
	}
	s.handler = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 3 << 20})
	return s, nil
}

func (s *Server) Close() error { return s.transfers.Close() }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	c, err := ReadConfig(s.options.ConfigPath)
	if err != nil {
		http.Error(w, "MCP configuration unavailable", http.StatusServiceUnavailable)
		return
	}
	if origins := r.Header.Values("Origin"); len(origins) > 0 && (len(origins) != 1 || !slices.Contains(c.AllowedOrigins, origins[0])) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	headers := r.Header.Values("Authorization")
	var key Credential
	var valid bool
	if len(headers) == 1 {
		kind, token, found := strings.Cut(headers[0], " ")
		if found && strings.EqualFold(kind, "Bearer") {
			key, valid = c.credential(token, time.Now())
		}
	}
	if !valid {
		w.Header().Set("WWW-Authenticate", `Bearer realm="clusterforge-mcp"`)
		http.Error(w, "valid MCP bearer token required", http.StatusUnauthorized)
		return
	}
	user, err := s.options.LookupUser(r.Context(), key.UserID)
	if err != nil {
		http.Error(w, "MCP identity unavailable", http.StatusUnauthorized)
		return
	}
	s.transfers.SetLimits(c.MaxFileBytes, c.MaxTotalBytes)
	select {
	case s.active <- struct{}{}:
		defer func() { <-s.active }()
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "MCP concurrency limit reached", http.StatusTooManyRequests)
		return
	}
	s.handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, identity{User: user, Credential: key})))
}

func (s *Server) addTool(server *mcp.Server, name, description string, schema Schema, readOnly, idempotent bool, fn func(context.Context, json.RawMessage) (*mcp.CallToolResult, error)) error {
	encoded, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	var js jsonschema.Schema
	if err = json.Unmarshal(encoded, &js); err != nil {
		return err
	}
	resolved, err := js.Resolve(nil)
	if err != nil {
		return fmt.Errorf("%s schema: %w", name, err)
	}
	destructive := !readOnly
	server.AddTool(&mcp.Tool{Name: name, Description: description, InputSchema: schema, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, DestructiveHint: &destructive, IdempotentHint: idempotent}}, func(ctx context.Context, req *mcp.CallToolRequest) (result *mcp.CallToolResult, err error) {
		start := time.Now()
		status := http.StatusOK
		defer func() {
			v, ok := ctx.Value(identityKey{}).(identity)
			if !ok {
				return
			}
			if result != nil && result.IsError {
				status = http.StatusBadRequest
				if m, ok := result.StructuredContent.(map[string]any); ok {
					if n, ok := m["status"].(int); ok {
						status = n
					}
				}
			}
			if err != nil {
				status = 500
			}
			if s.options.Audit != nil {
				s.options.Audit(context.WithoutCancel(ctx), Audit{ClientID: v.Credential.ClientID, UserID: v.User.ID, ToolName: name, RequestID: strings.TrimPrefix(randomID(), "mcp-transfer-"), Status: status, Duration: time.Since(start)})
			}
		}()
		if CurrentUser(ctx).ID == "" {
			return failure(401, "unauthorized", "MCP identity unavailable"), nil
		}
		raw := req.Params.Arguments
		if len(raw) == 0 {
			raw = json.RawMessage(`{}`)
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return failure(400, "invalid_request", "invalid tool arguments"), nil
		}
		if err := resolved.Validate(value); err != nil {
			return failure(400, "invalid_request", "arguments do not match tool inputSchema"), nil
		}
		result, err = fn(ctx, raw)
		if err != nil {
			slog.Error("MCP tool failed", "tool", name, "error", err)
			return failure(500, "internal_error", "MCP tool failed; inspect platform state before retrying a mutation"), nil
		}
		return result, nil
	})
	return nil
}

func result(value any, isError bool) *mcp.CallToolResult {
	data, _ := json.Marshal(value)
	return &mcp.CallToolResult{StructuredContent: value, Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}, IsError: isError}
}
func failure(status int, code, message string) *mcp.CallToolResult {
	return result(map[string]any{"status": status, "error": map[string]any{"code": code, "message": message}}, true)
}

func (s *Server) addTransferTools(server *mcp.Server) error {
	stringField := String("")
	idField := String("Opaque transferId returned by this server; scoped to this credential.")
	integer := Schema{"type": "integer", "minimum": 0}
	size := Schema{"type": "integer", "minimum": 0}
	defs := []struct {
		name, description    string
		schema               Schema
		readOnly, idempotent bool
	}{
		{"create", "Reserve an upload. Supply filename, total sizeBytes and lowercase sha256. Use write then finish; incomplete uploads expire after one hour.", Object(map[string]any{"filename": stringField, "mediaType": stringField, "sizeBytes": size, "sha256": stringField}, "filename", "sizeBytes", "sha256"), false, false},
		{"write", "Write at the next byte offset (maximum 1 MiB decoded). Repeating the same bytes at an earlier offset is safe. Returns nextOffset.", Object(map[string]any{"transferId": idField, "offset": integer, "contentBase64": stringField}, "transferId", "offset", "contentBase64"), false, true},
		{"finish", "Verify length and SHA-256, making the upload immutable and usable by a business tool.", Object(map[string]any{"transferId": idField}, "transferId"), false, true},
		{"read", "Read up to 1 MiB from a completed upload or output. Returns base64, nextOffset, eof and file metadata.", Object(map[string]any{"transferId": idField, "offset": integer, "length": Schema{"type": "integer", "minimum": 1, "maximum": ChunkBytes}}, "transferId"), true, true},
		{"delete", "Remove a temporary transfer owned by this credential. Does not delete platform assets.", Object(map[string]any{"transferId": idField}, "transferId"), false, false},
	}
	for _, def := range defs {
		d := def
		if err := s.addTool(server, "clusterforge_transfer_"+d.name, d.description, d.schema, d.readOnly, d.idempotent, func(ctx context.Context, raw json.RawMessage) (*mcp.CallToolResult, error) {
			var in struct {
				TransferID    string `json:"transferId"`
				Filename      string `json:"filename"`
				MediaType     string `json:"mediaType"`
				SizeBytes     int64  `json:"sizeBytes"`
				SHA256        string `json:"sha256"`
				Offset        int64  `json:"offset"`
				ContentBase64 string `json:"contentBase64"`
				Length        int    `json:"length"`
			}
			if err := strictJSON(raw, &in); err != nil {
				return failure(400, "invalid_request", "invalid transfer arguments"), nil
			}
			owner := ownerKey(ctx)
			var out any
			var err error
			switch d.name {
			case "create":
				out, err = s.transfers.Create(owner, in.Filename, in.MediaType, in.SizeBytes, in.SHA256)
			case "write":
				var data []byte
				data, err = base64.StdEncoding.DecodeString(in.ContentBase64)
				if err == nil {
					var n int64
					n, err = s.transfers.Write(owner, in.TransferID, in.Offset, data)
					out = map[string]any{"nextOffset": n}
				}
			case "finish":
				out, err = s.transfers.Finish(owner, in.TransferID)
			case "delete":
				err = s.transfers.Delete(owner, in.TransferID)
				out = map[string]bool{"deleted": err == nil}
			case "read":
				var f io.ReaderAt
				var info FileInfo
				var release func()
				f, info, release, err = s.transfers.Open(owner, in.TransferID)
				if err == nil {
					defer release()
					if in.Length == 0 {
						in.Length = ChunkBytes
					}
					if in.Offset > info.SizeBytes {
						err = errors.New("offset exceeds file length")
					} else {
						data := make([]byte, in.Length)
						var n int
						n, err = f.ReadAt(data, in.Offset)
						if errors.Is(err, io.EOF) {
							err = nil
						}
						out = map[string]any{"file": info, "contentBase64": base64.StdEncoding.EncodeToString(data[:n]), "nextOffset": in.Offset + int64(n), "eof": in.Offset+int64(n) >= info.SizeBytes}
					}
				}
			}
			if err != nil {
				return failure(400, "transfer_error", err.Error()), nil
			}
			return result(out, false), nil
		}); err != nil {
			return err
		}
	}
	return nil
}
