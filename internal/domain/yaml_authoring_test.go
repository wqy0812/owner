package domain

import (
	"strings"
	"testing"
)

func TestRollbackBindingDefaultsAndOverrides(t *testing.T) {
	r := boundRelease()
	r.Actions[1].PreCheckActionID, r.Actions[1].PostCheckActionID = "", ""
	if err := ValidateActionBindings(r, true); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []ActionKind{ActionInstall, ActionUpgrade, ActionConfigure} {
		source := ActionDefinition{ID: string(kind) + "-source", Kind: kind, PreCheckActionID: "restored"}
		r.Actions = append(r.Actions, source)
		check, err := RollbackPostCheck(r, r.Actions[1], source.ID)
		if err != nil || check.ID != "restored" {
			t.Fatalf("%s: %+v %v", kind, check, err)
		}
	}
	if _, err := RollbackPostCheck(r, r.Actions[1], ""); err == nil {
		t.Fatal("missing source accepted")
	}
	r.Actions[1].PostCheckActionID = "post"
	if check, err := RollbackPostCheck(r, r.Actions[1], ""); err != nil || check.ID != "post" {
		t.Fatalf("explicit override: %+v %v", check, err)
	}
}

func TestGeneratedComponentDirectoriesNeverNestOrCollide(t *testing.T) {
	var roots []string
	for i, id := range []string{"component-a", "component-b", "component-c"} {
		component := Component{ID: id, Slug: []string{"Same Name", "Same-Name", "same/name/child"}[i]}
		for _, version := range []string{"1", "2"} {
			r := ComponentRelease{ID: id + "-" + version, LineID: "line", LineName: "Stable", Version: version}
			root := GeneratedComponentWorkspaceRoot(component, r)
			if err := ValidateComponentWorkspaceRoot(root); err != nil {
				t.Fatal(err)
			}
			for _, previous := range roots {
				if strings.HasPrefix(root, previous) || strings.HasPrefix(previous, root) {
					t.Fatalf("overlap: %s %s", root, previous)
				}
			}
			roots = append(roots, root)
		}
	}
	for _, root := range []string{"managed/a/b/../c", "managed/a/b", "managed/a/b/c/d", "/managed/a/b/c", "managed/a/b/c\\d"} {
		if err := ValidateComponentWorkspaceRoot(root); err == nil {
			t.Fatalf("unsafe root accepted: %s", root)
		}
	}
}
