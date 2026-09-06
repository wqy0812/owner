package backup

import "testing"

func TestCatalogBranchScopeRejectsTamperedParent(t *testing.T) {
	cell := func(value string) DBCell { return DBCell{Kind: "text", Text: value} }
	for _, relation := range []struct{ parent, child, key string }{{"component_release_lines", "component_releases", "line_id"}, {"scenarios", "scenario_revisions", "scenario_id"}} {
		for _, parent := range []string{`{"architecture":["amd64"]}`, `{}`, `{"architecture":3}`, `null`} {
			catalog := Catalog{Tables: []TableDump{
				{Name: relation.parent, Columns: []string{"id", "environment_constraints_json"}, Rows: [][]DBCell{{cell("branch"), cell(parent)}}},
				{Name: relation.child, Columns: []string{"id", relation.key, "environment_constraints_json"}, Rows: [][]DBCell{{cell("version"), cell("branch"), cell(`{"architecture":["amd64","amd64"]}`)}}},
			}}
			err := validateCatalogBranchScopes(catalog)
			if parent == `{"architecture":["amd64"]}` && err != nil {
				t.Fatalf("equivalent %s scope rejected: %v", relation.parent, err)
			}
			if parent != `{"architecture":["amd64"]}` && err == nil {
				t.Fatalf("tampered %s scope accepted: %s", relation.parent, parent)
			}
		}
	}
}
