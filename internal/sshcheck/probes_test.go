package sshcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadOnlyProbesRejectShellInput(t *testing.T) {
	for _, value := range []string{"curl;id", "$(id)", "x\nwhoami", "'", "/tmp/../etc", "/"} {
		kind := "command"
		if strings.HasPrefix(value, "/") {
			kind = "path_absent"
		}
		if _, err := ProbeCommand(Probe{Kind: kind, Target: value}); err == nil {
			t.Fatalf("accepted unsafe probe %q", value)
		}
	}
	for _, p := range []Probe{{Kind: "command", Target: "python3"}, {Kind: "path_absent", Target: "/etc/cni/net.d/flannel.conf"}, {Kind: "tcp", Target: "[::1]:443"}, {Kind: "service_inactive", Target: "kubelet"}} {
		if _, err := ProbeCommand(p); err != nil {
			t.Errorf("valid probe %+v: %v", p, err)
		}
	}
}

func TestPathAbsentProbeRejectsDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "reserved")
	command, err := ProbeCommand(Probe{Kind: "path_absent", Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("sh", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("missing path should pass: %v %s", err, output)
	}
	if err := os.Symlink(filepath.Join(dir, "missing"), target); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("sh", "-c", command).CombinedOutput(); err == nil {
		t.Fatalf("dangling symlink must block path absence: %s", output)
	}
}

func TestHTTPProbeReadsOneByteAndKeepsErrorsBounded(t *testing.T) {
	for _, target := range []string{"http://user:password@host/file", "file:///etc/passwd", "https://host/file\ncommand"} {
		if _, err := ProbeCommand(Probe{Kind: "http", Target: target}); err == nil {
			t.Fatalf("unsafe endpoint %q", target)
		}
	}
	command, err := ProbeCommand(Probe{Kind: "http", Target: "https://files.example.test/file?identity=a", Interpreter: "/usr/bin/python3"})
	if err != nil || !strings.Contains(command, "response.read(1)") || !strings.Contains(command, "timeout=8") {
		t.Fatal(command, err)
	}
	command, err = ProbeCommand(Probe{Kind: "image_command", Target: "registry.test/tools@sha256:" + strings.Repeat("a", 64) + "#nc"})
	if err != nil || strings.Contains(command, "{{") || !strings.Contains(command, "image inspect") || !strings.Contains(command, "--network none") || strings.Contains(command, " pull ") {
		t.Fatal("image inspection can replace source", command, err)
	}
}

func TestRegistryTransportUsesExistingDockerPolicy(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python unavailable")
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	registry := strings.TrimPrefix(server.URL, "http://")
	for _, tc := range []struct {
		name, policy string
		pass         bool
	}{
		{"explicit insecure registry", fmt.Sprintf(`{"IndexConfigs":{%q:{"Secure":false}}}`, registry), true},
		{"secure registry never falls back", fmt.Sprintf(`{"IndexConfigs":{%q:{"Secure":true}}}`, registry), false},
		{"configured insecure address range", `{"InsecureRegistryCIDRs":["127.0.0.0/8"]}`, true},
		{"unknown configuration fails closed", `invalid`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nprintf '%s' "+quoteProbe(tc.policy)), 0700); err != nil {
				t.Fatal(err)
			}
			command, err := ProbeCommand(Probe{Kind: "registry", Target: "https://" + registry + "/v2/", RegistryTransportFromDocker: true})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "-c", command)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
			before := calls.Load()
			output, err := cmd.CombinedOutput()
			if (err == nil) != tc.pass {
				t.Fatalf("unexpected result: %v %s", err, output)
			}
			if !tc.pass && calls.Load() != before {
				t.Fatal("unconfigured HTTP fallback reached registry")
			}
		})
	}
}

func TestImageCommandProbeRealAnsible(t *testing.T) {
	binary := os.Getenv("CLUSTERFORGE_JOB_TEST_ANSIBLE")
	if binary == "" {
		t.Skip("requires real Ansible")
	}
	dir := t.TempDir()
	reference := "registry.test/tools@sha256:" + strings.Repeat("a", 64)
	id := "sha256:" + strings.Repeat("b", 64)
	docker := "#!/usr/bin/env python3\nimport json,sys\na=sys.argv[1:]\nif a==['image','inspect'," + strconv.Quote(reference) + "]:\n print(json.dumps([{'Id':" + strconv.Quote(id) + "}]))\nelif a[:7]==['run','--rm','--read-only','--network','none','--entrypoint','/bin/sh'] and a[7]==" + strconv.Quote(id) + ":\n assert a[8:]==['-c',\"command -v 'nc'\"]\n print('/usr/bin/nc')\nelse:\n raise SystemExit('Unexpected Docker action')\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(docker), 0700); err != nil {
		t.Fatal(err)
	}
	command, err := ProbeCommand(Probe{Kind: "image_command", Target: reference + "#nc"})
	if err != nil {
		t.Fatal(err)
	}
	playbook, _ := json.Marshal([]any{map[string]any{"hosts": "all", "gather_facts": false, "tasks": []any{map[string]any{"name": "Inspect the exact local image without downloading", "shell": command, "changed_when": false}}}})
	filename := filepath.Join(dir, "probe.yml")
	if err := os.WriteFile(filename, playbook, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-i", "localhost,", "-c", "local", filename)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Ansible rejected generated prerequisite: %v\n%s", err, output)
	}
}
