// Package mcpcatalog generates the MCP catalog from the registered REST routes
// and the actual request DTOs. Generation is checked by API tests, never at runtime.
package mcpcatalog

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

var Exclusions = map[string]string{
	"POST /api/v1/session/switch": "MCP credentials bind identity; switching is forbidden",
	"GET /api/v1/session/users":   "Demo login identity enumeration is not an MCP capability",
	"GET /api/v1/events":          "Browser SSE transport; use run activity and workbench polling",
}

type parsedFunc struct {
	decl    *ast.FuncDecl
	imports map[string]string
}

func Generate(root string) ([]byte, error) {
	fset := token.NewFileSet()
	funcs := map[string]parsedFunc{}
	paths, err := filepath.Glob(filepath.Join(root, "internal/api/*.go"))
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") || strings.HasSuffix(path, "mcp_catalog_generated.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, err
		}
		imports := map[string]string{}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			name := filepath.Base(p)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			imports[name] = p
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				funcs[fn.Name.Name] = parsedFunc{fn, imports}
			}
		}
	}
	routes, ok := funcs["routes"]
	if !ok {
		return nil, fmt.Errorf("routes function missing")
	}
	var entries []string
	usedImports := map[string]string{"mcpserver": "codex/platform-demo/internal/mcpserver", "reflect": "reflect"}
	var generationErr error
	ast.Inspect(routes.decl.Body, func(node ast.Node) bool {
		if generationErr != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "HandleFunc" && sel.Sel.Name != "Handle") {
			return true
		}
		if sel.Sel.Name != "HandleFunc" || len(call.Args) != 2 {
			generationErr = fmt.Errorf("unsupported route registration at %s; update MCP generation before adding this route", fset.Position(call.Pos()))
			return false
		}
		literal, ok := call.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			generationErr = fmt.Errorf("route pattern must be a string literal at %s", fset.Position(call.Pos()))
			return false
		}
		pattern, _ := strconv.Unquote(literal.Value)
		if _, excluded := Exclusions[pattern]; excluded {
			return true
		}
		handler, ok := call.Args[1].(*ast.SelectorExpr)
		if !ok {
			generationErr = fmt.Errorf("unsupported route handler: %s", pattern)
			return false
		}
		fn, ok := funcs[handler.Sel.Name]
		if !ok {
			generationErr = fmt.Errorf("missing handler %s", handler.Sel.Name)
			return false
		}
		// Some routes select a Run kind then delegate the complete request to a
		// shared handler. Infer the decoder from that handler, not the wrapper.
		visited := map[string]bool{}
		for len(fn.decl.Body.List) == 1 {
			expr, ok := fn.decl.Body.List[0].(*ast.ExprStmt)
			if !ok {
				break
			}
			delegate, ok := expr.X.(*ast.CallExpr)
			if !ok {
				break
			}
			selector, ok := delegate.Fun.(*ast.SelectorExpr)
			if !ok {
				break
			}
			receiver, ok := selector.X.(*ast.Ident)
			if !ok || receiver.Name != "h" {
				break
			}
			next, ok := funcs[selector.Sel.Name]
			if !ok {
				break
			}
			if visited[selector.Sel.Name] {
				generationErr = fmt.Errorf("recursive request handler %s", handler.Sel.Name)
				return false
			}
			visited[selector.Sel.Name] = true
			fn = next
		}
		var bodyType ast.Expr
		var decodedName string
		queries, fields, files := []string{}, []string{}, []string{}
		ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
			c, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := c.Fun.(*ast.Ident); ok {
				if id.Name == "decodeJSON" && len(c.Args) == 2 {
					if u, ok := c.Args[1].(*ast.UnaryExpr); ok {
						if n, ok := u.X.(*ast.Ident); ok {
							decodedName = n.Name
						}
					}
				}
				if len(c.Args) == 2 {
					if lit, ok := c.Args[1].(*ast.BasicLit); ok {
						v, _ := strconv.Unquote(lit.Value)
						if id.Name == "optionalQueryValue" {
							queries = appendUnique(queries, v)
						}
						if id.Name == "optionalFormValue" {
							fields = appendUnique(fields, v)
						}
					}
				}
			}
			if s, ok := c.Fun.(*ast.SelectorExpr); ok && len(c.Args) == 1 {
				if lit, ok := c.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					v, _ := strconv.Unquote(lit.Value)
					switch s.Sel.Name {
					case "Get", "Has":
						queries = appendUnique(queries, v)
					case "FormValue":
						fields = appendUnique(fields, v)
					case "FormFile":
						files = appendUnique(files, v)
					}
				}
			}
			return true
		})
		if decodedName != "" {
			ast.Inspect(fn.decl.Body, func(node ast.Node) bool {
				if v, ok := node.(*ast.ValueSpec); ok {
					for _, n := range v.Names {
						if n.Name == decodedName {
							bodyType = v.Type
						}
					}
				}
				return true
			})
			if bodyType == nil {
				generationErr = fmt.Errorf("cannot infer request DTO for %s", pattern)
				return false
			}
		}
		// These values are obtained by shared helpers or a variable query key.
		if handler.Sel.Name == "listRuns" {
			queries = appendUnique(queries, "page")
			queries = appendUnique(queries, "pageSize")
		}
		if handler.Sel.Name == "uploadComponentArtifact" {
			fields = appendUnique(fields, "sha256")
			files = appendUnique(files, "checksumFile")
		}
		body := "nil"
		if bodyType != nil {
			var b bytes.Buffer
			_ = format.Node(&b, fset, bodyType)
			body = "mcpserver.BodySchema(reflect.TypeOf((*" + b.String() + ")(nil)).Elem())"
			ast.Inspect(bodyType, func(n ast.Node) bool {
				if s, ok := n.(*ast.SelectorExpr); ok {
					if id, ok := s.X.(*ast.Ident); ok {
						if p, ok := fn.imports[id.Name]; ok {
							usedImports[id.Name] = p
						}
					}
				}
				return true
			})
		}
		sort.Strings(queries)
		sort.Strings(fields)
		name := handler.Sel.Name
		entries = append(entries, fmt.Sprintf("h.mcpOperation(%q, %q, %q, %s, %#v, %#v, %#v, h.%s),", name, "clusterforge_"+snake(name), pattern, body, queries, fields, files, name))
		return true
	})
	if generationErr != nil {
		return nil, generationErr
	}
	var out bytes.Buffer
	out.WriteString("// Code generated by go run ./cmd/mcp-catalog; DO NOT EDIT.\npackage api\n\nimport (\n")
	var names []string
	for name := range usedImports {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&out, "%s %q\n", name, usedImports[name])
	}
	out.WriteString(")\n\nfunc (h *Handler) mcpOperations() []mcpserver.Operation { return []mcpserver.Operation{\n")
	for _, entry := range entries {
		out.WriteString(entry + "\n")
	}
	out.WriteString("}}\n")
	return format.Source(out.Bytes())
}

func appendUnique(values []string, value string) []string {
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}
func snake(s string) string {
	r := []rune(s)
	var b strings.Builder
	for i, c := range r {
		if unicode.IsUpper(c) {
			if i > 0 && (unicode.IsLower(r[i-1]) || (i+1 < len(r) && unicode.IsLower(r[i+1]))) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(c))
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}

// Markdown keeps the operator-facing inventory tied to the same generated code.
func Markdown(generated []byte) ([]byte, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "catalog.go", generated, 0)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	out.WriteString("# MCP 工具覆盖清单\n\n由 `go run ./cmd/mcp-catalog` 生成，勿手动编辑。连接、认证和文件操作见 [MCP 接入](mcp.md)。\n\n| 工具 | 对应业务接口 |\n| --- | --- |\n")
	count := 0
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		s, ok := c.Fun.(*ast.SelectorExpr)
		if !ok || s.Sel.Name != "mcpOperation" {
			return true
		}
		name, _ := strconv.Unquote(c.Args[1].(*ast.BasicLit).Value)
		pattern, _ := strconv.Unquote(c.Args[2].(*ast.BasicLit).Value)
		fmt.Fprintf(&out, "| `%s` | `%s` |\n", name, pattern)
		count++
		return true
	})
	fmt.Fprintf(&out, "\n业务工具共 %d 个；另有 `clusterforge_transfer_create`、`clusterforge_transfer_write`、`clusterforge_transfer_finish`、`clusterforge_transfer_read`、`clusterforge_transfer_delete` 五个文件工具。\n\n", count)
	out.WriteString("明确排除：`POST /api/v1/session/switch`、`GET /api/v1/session/users`、`GET /api/v1/events`。令牌绑定身份；通过运行详情与增量活动查询跟踪任务。\n")
	return []byte(out.String()), nil
}
