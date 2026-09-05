package domain

import (
	"testing"
)

func boundRelease() ComponentRelease {
	return ComponentRelease{ID: "release", Actions: []ActionDefinition{
		{ID: "install", ReleaseID: "release", Kind: ActionInstall, PreCheckActionID: "pre", PostCheckActionID: "post"},
		{ID: "rollback", ReleaseID: "release", Kind: ActionRollback, PreCheckActionID: "pre", PostCheckActionID: "restored"},
		{ID: "pre", ReleaseID: "release", Kind: ActionCheck}, {ID: "post", ReleaseID: "release", Kind: ActionCheck}, {ID: "restored", ReleaseID: "release", Kind: ActionCheck},
	}}
}
func TestActionBindings(t *testing.T) {
	for _, tc := range []struct {
		name     string
		change   func(*ComponentRelease)
		complete bool
		bad      bool
	}{
		{"shared check", func(*ComponentRelease) {}, true, false},
		{"draft incomplete", func(r *ComponentRelease) {
			r.Actions = r.Actions[:1]
			r.Actions[0].PreCheckActionID = ""
			r.Actions[0].PostCheckActionID = ""
		}, false, false},
		{"missing rollback", func(r *ComponentRelease) { r.Actions = append(r.Actions[:1], r.Actions[2:]...) }, true, true},
		{"missing precheck", func(r *ComponentRelease) { r.Actions[0].PreCheckActionID = "" }, true, true},
		{"missing postcheck", func(r *ComponentRelease) { r.Actions[0].PostCheckActionID = "" }, true, true},
		{"foreign check", func(r *ComponentRelease) { r.Actions[2].ReleaseID = "other" }, true, true},
		{"not a check", func(r *ComponentRelease) { r.Actions[0].PreCheckActionID = "rollback" }, true, true},
		{"recursive check", func(r *ComponentRelease) { r.Actions[2].PreCheckActionID = "post" }, true, true},
		{"deleted reference", func(r *ComponentRelease) { r.Actions = r.Actions[:4] }, false, true},
		{"duplicate execute kind", func(r *ComponentRelease) {
			r.Actions = append(r.Actions, ActionDefinition{ID: "install2", Kind: ActionInstall})
		}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := boundRelease()
			tc.change(&r)
			if err := ValidateActionBindings(r, tc.complete); (err != nil) != tc.bad {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
