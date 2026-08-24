package service

import "testing"

func TestEnvironmentConstraintsUseExactFirstVersionContract(t *testing.T) {
	constraints := map[string]any{
		"architecture":    []any{"amd64"},
		"operatingSystem": []any{"Kylin"},
		"ipFamily":        []any{"IPv4"},
	}
	canonical := map[string]any{
		"architecture":    "amd64",
		"operatingSystem": "Kylin",
		"ipFamily":        "IPv4",
	}
	if err := validateEnvironmentConstraints(constraints, canonical); err != nil {
		t.Fatalf("canonical facts rejected: %v", err)
	}

	for name, facts := range map[string]map[string]any{
		"old field names":  {"arch": "amd64", "os": "Kylin", "network": "IPv4"},
		"old value casing": {"architecture": "amd64", "operatingSystem": "Kylin", "ipFamily": "ipv4"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateEnvironmentConstraints(constraints, facts); err == nil {
				t.Fatal("non-canonical environment facts were accepted")
			}
		})
	}
}
