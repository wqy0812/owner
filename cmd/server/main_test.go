package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDotEnvUsesValidUnsetValuesWithoutOverridingEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	contents := "# comment\nSERVER_TEST_VALUE=' from-file '\nSERVER_TEST_EXISTING=from-file\ninvalid-line\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SERVER_TEST_EXISTING", "from-environment")
	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("SERVER_TEST_VALUE"); got != " from-file " {
		t.Fatalf("dotenv value=%q", got)
	}
	if got := os.Getenv("SERVER_TEST_EXISTING"); got != "from-environment" {
		t.Fatalf("existing environment was overwritten: %q", got)
	}
	if err := loadDotEnv(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Fatalf("missing dotenv file error=%v", err)
	}
}

func TestEnvironmentConfigurationHelpersFailClosedToDefaults(t *testing.T) {
	t.Setenv("SERVER_TEST_VALUE", " configured ")
	if got := envOr("SERVER_TEST_VALUE", "fallback"); got != "configured" {
		t.Fatalf("envOr=%q", got)
	}
	t.Setenv("SERVER_TEST_DURATION", "250ms")
	if got := envDuration("SERVER_TEST_DURATION", time.Second); got != 250*time.Millisecond {
		t.Fatalf("envDuration=%s", got)
	}
	t.Setenv("SERVER_TEST_DURATION", "invalid")
	if got := envDuration("SERVER_TEST_DURATION", time.Second); got != time.Second {
		t.Fatalf("invalid duration=%s", got)
	}
	t.Setenv("SERVER_TEST_INT", "64")
	if got := envInt("SERVER_TEST_INT", 10); got != 64 {
		t.Fatalf("envInt=%d", got)
	}
	t.Setenv("SERVER_TEST_INT", "0")
	if got := envInt("SERVER_TEST_INT", 10); got != 10 {
		t.Fatalf("non-positive int=%d", got)
	}
}
