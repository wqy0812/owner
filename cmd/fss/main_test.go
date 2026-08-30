package main

import "testing"

func TestEnvOrTrimsConfiguredValueAndUsesFallback(t *testing.T) {
	t.Setenv("FSS_TEST_VALUE", " configured ")
	if got := envOr("FSS_TEST_VALUE", "fallback"); got != "configured" {
		t.Fatalf("configured value=%q", got)
	}
	t.Setenv("FSS_TEST_VALUE", "   ")
	if got := envOr("FSS_TEST_VALUE", "fallback"); got != "fallback" {
		t.Fatalf("fallback value=%q", got)
	}
}
