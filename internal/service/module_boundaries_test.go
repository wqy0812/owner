package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
	"testing"
)

// Every capability panics if invoked: assembly must validate presence without
// invoking Ansible, inspecting workspaces, or accessing the database.
type assemblyOnlyRunner struct {
	WorkspaceInspector
	RuntimeInspector
	JobBackend
}

func TestPlatformAssemblyRequiresExplicitRunnerPorts(t *testing.T) {
	backend := &assemblyOnlyRunner{}
	valid := RunnerDependencies{Workspaces: backend, Runtime: backend, Jobs: backend}
	var typedNil *assemblyOnlyRunner
	for _, field := range []string{"Workspaces", "Runtime", "Jobs"} {
		for _, typed := range []bool{false, true} {
			runners := valid
			value := reflect.ValueOf(&runners).Elem().FieldByName(field)
			if typed {
				value.Set(reflect.ValueOf(typedNil))
			} else {
				value.SetZero()
			}
			p, err := NewPlatform(nil, runners, nil)
			if err == nil || p != nil || !strings.Contains(err.Error(), strings.ToLower(field)) {
				t.Fatalf("missing %s (typed nil=%v): platform=%v err=%v", field, typed, p, err)
			}
		}
	}
	p, err := NewPlatform(nil, valid, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.catalog.workspace != p.scenarios.workspace || p.scenarios.workspace != p.execution.workspace || p.execution.workspace != p.releases.workspace || p.releases.workspace != p.releaseRules.workspace {
		t.Fatal("workspace mutations and publication must share one lock and root")
	}
}

func TestBusinessModulesDoNotDependOnPlatformOrConcreteStore(t *testing.T) {
	files, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".go") || strings.HasSuffix(file.Name(), "_test.go") {
			continue
		}
		fs := token.NewFileSet()
		f, err := parser.ParseFile(fs, file.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if fn.Name.Name == "NewPlatform" {
					continue
				}
				// A receiver of Platform is allowed only in the composition root.
				if fn.Recv != nil && file.Name() == "platform.go" {
					continue
				}
			}
			ast.Inspect(decl, func(node ast.Node) bool {
				if spec, ok := node.(*ast.TypeSpec); ok && spec.Name.Name == "Platform" {
					return false
				}
				if id, ok := node.(*ast.Ident); ok && id.Name == "Platform" {
					t.Errorf("business object retains or calls Platform at %s", fs.Position(id.Pos()))
				}
				if selector, ok := node.(*ast.SelectorExpr); ok && selector.Sel.Name == "Store" {
					if id, ok := selector.X.(*ast.Ident); ok && id.Name == "store" {
						t.Errorf("concrete Store outside assembly at %s", fs.Position(node.Pos()))
					}
				}
				if assertion, ok := node.(*ast.TypeAssertExpr); ok && assertion.Type != nil {
					ast.Inspect(assertion.Type, func(n ast.Node) bool {
						if id, ok := n.(*ast.Ident); ok {
							switch id.Name {
							case "WorkspaceInspector", "RuntimeInspector", "JobBackend", "Digest", "DigestPlan", "ValidatePlaybooks", "RuntimeIdentity", "BuildJob", "RunBundle":
								t.Errorf("runtime runner capability discovery at %s", fs.Position(id.Pos()))
							}
						}
						return true
					})
				}
				return true
			})
		}
	}
}

func TestModuleDependencyGraphIsAcyclic(t *testing.T) {
	backend := &assemblyOnlyRunner{}
	p, err := NewPlatform(nil, RunnerDependencies{Workspaces: backend, Runtime: backend, Jobs: backend}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	pkg := reflect.TypeOf(Platform{}).PkgPath()
	visiting, done := map[reflect.Type]bool{}, map[reflect.Type]bool{}
	var visit func(reflect.Value, []string)
	visit = func(value reflect.Value, path []string) {
		if value.Kind() == reflect.Interface {
			if value.IsNil() {
				return
			}
			value = value.Elem()
		}
		node := value.Type()
		if node.Kind() != reflect.Pointer || node.Elem().Kind() != reflect.Struct || node.Elem().PkgPath() != pkg || value.IsNil() {
			return
		}
		if visiting[node] {
			t.Fatalf("dependency cycle: %s -> %s", strings.Join(path, " -> "), node.Elem().Name())
		}
		if done[node] {
			return
		}
		visiting[node] = true
		path = append(path, node.Elem().Name())
		for i := 0; i < node.Elem().NumField(); i++ {
			visit(value.Elem().Field(i), path)
		}
		visiting[node], done[node] = false, true
	}
	visit(reflect.ValueOf(p), nil)
	visit(reflect.ValueOf(newReadinessEvaluation(p.releaseRules)), nil)
}

func TestPlatformExposesOnlyAssemblyAndModuleAccess(t *testing.T) {
	allowed := strings.Fields("Preparations Identity Catalog Scenarios Environments Execution ReadModel Releases PlatformOptions Hub Audit ConfigureEnvironmentSSHChecker ConfigureEnvironmentHealthDialer ConfigurePlaybookRoot ConfigureImageBuilder ConfigureDeliveryAdapters ConfigurePublicationBackup ConfigurePublicationBackupHealth NotifyPublicationBackupStatus ConfigureRunArchives StartRunArchiveWorker Start Close")
	methods := map[string]bool{}
	for _, name := range allowed {
		methods[name] = true
	}
	typ := reflect.TypeOf((*Platform)(nil))
	for i := 0; i < typ.NumMethod(); i++ {
		if !methods[typ.Method(i).Name] {
			t.Errorf("business command on Platform: %s", typ.Method(i).Name)
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeOf((*plannerStore)(nil)).Elem(), reflect.TypeOf((*rollbackStore)(nil)).Elem()} {
		for i := 0; i < typ.NumMethod(); i++ {
			name := typ.Method(i).Name
			if name == "DB" || strings.HasPrefix(name, "Create") || strings.HasPrefix(name, "Save") || strings.HasPrefix(name, "Update") || strings.HasPrefix(name, "Set") || strings.HasPrefix(name, "Delete") || strings.HasPrefix(name, "Record") {
				t.Errorf("planning has a write dependency: %s.%s", typ.Name(), name)
			}
		}
	}
}
