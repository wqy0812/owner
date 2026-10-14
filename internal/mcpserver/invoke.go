package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const inlineResultBytes = 256 << 10

type arguments struct {
	Path  map[string]string `json:"path"`
	Query map[string]string `json:"query"`
	Body  json.RawMessage   `json:"body"`
	Files map[string]struct {
		TransferID string `json:"transferId"`
	} `json:"files"`
}

func (s *Server) invoke(ctx context.Context, op Operation, raw json.RawMessage) (*mcp.CallToolResult, error) {
	var args arguments
	if err := strictJSON(raw, &args); err != nil {
		return failure(400, "invalid_request", "invalid path, query, body or files"), nil
	}
	method, path, _ := strings.Cut(op.Pattern, " ")
	for name, value := range args.Path {
		if value == "" || strings.ContainsRune(value, '\x00') {
			return failure(400, "invalid_request", "path values must be nonempty identifiers without NUL"), nil
		}
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(value))
	}
	query := url.Values{}
	for key, value := range args.Query {
		query.Set(key, value)
	}
	owner := ownerKey(ctx)
	var body io.Reader = bytes.NewReader(args.Body)
	contentType := "application/json"
	var pipe *io.PipeReader
	var uploadDone chan error
	if len(args.Files) > 0 {
		var fields map[string]string
		if len(args.Body) > 0 {
			if err := strictJSON(args.Body, &fields); err != nil {
				return failure(400, "invalid_request", "multipart body fields must be strings"), nil
			}
		}
		type inputFile struct {
			name string
			file *os.File
			info FileInfo
		}
		var files []inputFile
		for field, ref := range args.Files {
			f, info, release, err := s.transfers.Open(owner, ref.TransferID)
			if err != nil {
				return failure(400, "transfer_error", err.Error()), nil
			}
			defer release()
			files = append(files, inputFile{field, f, info})
		}
		var writer *io.PipeWriter
		pipe, writer = io.Pipe()
		mw := multipart.NewWriter(writer)
		contentType = mw.FormDataContentType()
		body = pipe
		uploadDone = make(chan error, 1)
		go func() {
			err := func() error {
				for k, v := range fields {
					if err := mw.WriteField(k, v); err != nil {
						return err
					}
				}
				for _, f := range files {
					part, err := mw.CreateFormFile(f.name, f.info.Filename)
					if err != nil {
						return err
					}
					if _, err := io.Copy(part, f.file); err != nil {
						return err
					}
				}
				return mw.Close()
			}()
			writer.CloseWithError(err)
			uploadDone <- err
		}()
		defer func() { pipe.Close(); <-uploadDone }()
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://clusterforge.internal"+path+"?"+query.Encode(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	for name, value := range args.Path {
		req.SetPathValue(name, value)
	}
	defer func() {
		req.Body.Close()
		if req.MultipartForm != nil {
			req.MultipartForm.RemoveAll()
		}
	}()
	file, info, err := s.transfers.Output(owner)
	if err != nil {
		return failure(503, "transfer_capacity", "result storage unavailable; no business operation was invoked"), nil
	}
	keep := false
	defer func() {
		file.Close()
		if !keep {
			_ = s.transfers.Delete(owner, info.TransferID)
		}
	}()
	w := &responseFile{header: http.Header{}, file: file, limit: info.SizeBytes}
	op.Handle(w, req)
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if err := file.Close(); err != nil && w.err == nil {
		w.err = err
	}
	if w.err != nil {
		return failure(500, "result_unavailable", "operation was invoked but its result could not be stored; inspect platform state before retrying"), nil
	}
	mediaType := w.header.Get("Content-Type")
	if w.size <= inlineResultBytes && strings.Contains(mediaType, "application/json") {
		data, err := os.ReadFile(file.Name())
		if err != nil {
			return nil, err
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err == nil {
			return result(map[string]any{"status": w.status, "response": value}, w.status >= 400), nil
		}
	}
	if w.size == 0 {
		return result(map[string]any{"status": w.status}, w.status >= 400), nil
	}
	filename := "result.bin"
	if strings.Contains(mediaType, "json") {
		filename = "result.json"
	}
	if _, params, err := mime.ParseMediaType(w.header.Get("Content-Disposition")); err == nil && params["filename"] != "" {
		filename = params["filename"]
	}
	info, err = s.transfers.SealOutput(owner, info.TransferID, filename, mediaType, w.size)
	if err != nil {
		return nil, err
	}
	keep = true
	return result(map[string]any{"status": w.status, "file": info, "message": "Read the complete result with clusterforge_transfer_read; no content was truncated."}, w.status >= 400), nil
}

type responseFile struct {
	header      http.Header
	file        *os.File
	status      int
	size, limit int64
	err         error
}

func (w *responseFile) Header() http.Header { return w.header }
func (w *responseFile) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *responseFile) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.status == 0 {
		w.status = 200
	}
	if int64(len(p)) > w.limit-w.size {
		w.err = errors.New("MCP result size limit exceeded")
		return 0, w.err
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	w.err = err
	return n, err
}

// OperationSchema wraps route-specific inputs without allowing arbitrary paths
// or headers. Query values retain their exact HTTP string semantics.
func OperationSchema(pattern string, body Schema, queries, fields, files, requiredBody []string) Schema {
	props := map[string]any{}
	var required []string
	paths := map[string]any{}
	var pathNames []string
	for _, segment := range strings.Split(pattern, "/") {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			name := segment[1 : len(segment)-1]
			paths[name] = Schema{"type": "string", "minLength": 1}
			pathNames = append(pathNames, name)
		}
	}
	if len(paths) > 0 {
		props["path"] = Object(paths, pathNames...)
		required = append(required, "path")
	}
	if len(queries) > 0 {
		q := map[string]any{}
		for _, name := range queries {
			q[name] = String("HTTP query value as a string")
		}
		props["query"] = Object(q)
	}
	if body != nil {
		if len(requiredBody) > 0 {
			body["required"] = requiredBody
		}
		props["body"] = body
		required = append(required, "body")
	}
	if len(fields) > 0 {
		p := map[string]any{}
		for _, name := range fields {
			p[name] = String("Multipart form field; JSON-valued fields must be JSON encoded strings")
		}
		props["body"] = Object(p)
	}
	if len(files) > 0 {
		p := map[string]any{}
		for _, name := range files {
			p[name] = Object(map[string]any{"transferId": String("Completed upload reference")}, "transferId")
		}
		props["files"] = Object(p, files[0])
		required = append(required, "files")
	}
	return Object(props, required...)
}

func DescribeOperation(name, pattern, notes string, readOnly bool) string {
	kind := "Mutates platform state or may execute external actions. Do not automatically retry after an uncertain outcome."
	if readOnly {
		kind = "Reads or previews platform state; does not submit an execution."
	}
	return fmt.Sprintf("%s (%s). %s %s Permissions and owner checks are identical to the platform UI.", name, pattern, kind, notes)
}
