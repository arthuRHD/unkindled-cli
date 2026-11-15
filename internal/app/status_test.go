package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/arthuRHD/unkindled-cli/internal/api"
	"github.com/arthuRHD/unkindled-cli/internal/sshconf"
)

func statusFixture(t *testing.T, herdrList string) *testEnv {
	t.Helper()
	env := newTestEnv(t)
	list := []api.Workstation{
		{ID: "ws-1", Name: "dev box", State: "running", Plan: "pro", IPv4: "203.0.113.9"},
		{ID: "ws-2", Name: "prod", State: "stopped", Plan: "team", IPv4: "198.51.100.4"},
	}
	connections := map[string]api.Connection{
		"ws-1": {IPv4: "203.0.113.9", User: "ubuntu", SSHHostAlias: "unkindled-dev box", PlatformGeneratedKey: true},
		"ws-2": {IPv4: "198.51.100.4", User: "ubuntu", SSHHostAlias: "unkindled-prod", PlatformGeneratedKey: true},
	}
	server := newControlPlane(t, list, connections, testPrivateKey, nil)
	env.saveConfig(server.URL, "test-token")

	keyPath := env.keyPath("dev-box")
	env.writeFile(keyPath, []byte(testPrivateKey+"\n"), 0o600)
	block, err := sshconf.Build(sshconf.Block{
		ID: "ws-1", Alias: "unkindled-dev-box", HostName: "203.0.113.9", User: "ubuntu", IdentityFile: keyPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	env.writeFile(env.sshConfigPath(), []byte("# preserved\n\n"+block), 0o600)
	installFakeHerdr(t, env, herdrList, env.bin+"/herdr.log")
	return env
}

func TestStatusReportsConfiguration(t *testing.T) {
	env := statusFixture(t, `[{"id":"1","label":"dev box","target":"unkindled-dev-box"}]`)

	code, stdout, stderr := env.run("status", "--json")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	report := decodeJSON[statusReport](t, stdout)
	if report.API == "" || !report.Herdr.Available {
		t.Fatalf("unexpected report header: %+v", report)
	}
	if len(report.Workstations) != 2 {
		t.Fatalf("workstations = %d, want 2", len(report.Workstations))
	}
	dev := report.Workstations[0]
	if !dev.Configured || !dev.SSH.OK || !dev.Key.OK || !dev.Herdr.Present {
		t.Fatalf("configured workstation reported as missing: %+v", dev)
	}
	prod := report.Workstations[1]
	if prod.Configured || prod.SSH.OK || prod.Key.OK || prod.Herdr.Present {
		t.Fatalf("unconfigured workstation reported as configured: %+v", prod)
	}
	if len(report.Missing) != 1 || report.Missing[0].ID != "ws-2" {
		t.Fatalf("missing = %+v", report.Missing)
	}

	code, stdout, stderr = env.run("status")
	if code != 0 {
		t.Fatalf("human status exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"1 workstation(s) need configuration",
		"prod",
		"unkindled ssh sync",
		"unkindled herdr sync",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("status output missing %q:\n%s", want, stdout)
		}
	}
}

func TestStatusWithoutHerdr(t *testing.T) {
	env := newTestEnv(t)
	ws := api.Workstation{ID: "ws-1", Name: "solo", State: "running", Plan: "pro", IPv4: "203.0.113.9"}
	conn := api.Connection{IPv4: "203.0.113.9", User: "ubuntu", PlatformGeneratedKey: true}
	server := newControlPlane(t, []api.Workstation{ws}, map[string]api.Connection{"ws-1": conn}, testPrivateKey, nil)
	env.saveConfig(server.URL, "test-token")
	keyPath := env.keyPath("solo")
	env.writeFile(keyPath, []byte(testPrivateKey+"\n"), 0o600)
	block, err := sshconf.Build(sshconf.Block{
		ID: "ws-1", Alias: "unkindled-solo", HostName: "203.0.113.9", User: "ubuntu", IdentityFile: keyPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	env.writeFile(env.sshConfigPath(), []byte(block), 0o600)

	code, stdout, stderr := env.run("status", "--json")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	report := decodeJSON[statusReport](t, stdout)
	if report.Herdr.Available {
		t.Fatal("herdr should be unavailable")
	}
	if !report.Workstations[0].Configured {
		t.Fatalf("SSH-only workstation should be configured: %+v", report.Workstations[0])
	}

	code, stdout, _ = env.run("status")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "herdr: not installed") {
		t.Fatalf("missing herdr notice:\n%s", stdout)
	}
	if !strings.Contains(stdout, "All workstations are configured locally.") {
		t.Fatalf("unexpected summary:\n%s", stdout)
	}
}

func TestStatusFlagsBadKeyMode(t *testing.T) {
	env := statusFixture(t, `[{"id":"1","label":"dev box"},{"id":"2","label":"prod"}]`)
	env.writeFile(env.keyPath("dev-box"), []byte(testPrivateKey+"\n"), 0o644)

	code, stdout, stderr := env.run("status", "--json")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	report := decodeJSON[statusReport](t, stdout)
	dev := report.Workstations[0]
	if dev.Key.OK {
		t.Fatal("key with mode 0644 should not be OK")
	}
	if !strings.Contains(strings.Join(dev.Issues, "; "), "0644") {
		t.Fatalf("issues = %+v", dev.Issues)
	}
}

func TestStatusUnauthorized(t *testing.T) {
	env := newTestEnv(t)
	server := newAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
	})
	env.saveConfig(server.URL, "test-token")

	code, _, stderr := env.run("status")
	if code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(stderr, "unkindled login") {
		t.Fatalf("missing login hint:\n%s", stderr)
	}
}

func TestStatusWorkstationsAlias(t *testing.T) {
	env := statusFixture(t, `[]`)
	code, stdout, stderr := env.run("workstations", "--json")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	var report map[string]any
	report = decodeJSON[map[string]any](t, stdout)
	if _, ok := report["workstations"]; !ok {
		t.Fatalf("unexpected JSON:\n%s", stdout)
	}
}

func TestStatusMalformedSSHConfig(t *testing.T) {
	env := statusFixture(t, `[]`)
	env.writeFile(env.sshConfigPath(), []byte("# unkindled:ws-1\nHost broken\n"), 0o600)
	code, stdout, stderr := env.run("status", "--json")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	report := decodeJSON[statusReport](t, stdout)
	issues := strings.Join(report.Workstations[0].Issues, "; ")
	if !strings.Contains(issues, "malformed managed block") {
		t.Fatalf("issues = %s", issues)
	}
}

func TestStatusKeyMissing(t *testing.T) {
	env := statusFixture(t, `[{"label":"dev box"}]`)
	if err := os.Remove(env.keyPath("dev-box")); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := env.run("status", "--json")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	report := decodeJSON[statusReport](t, stdout)
	if report.Workstations[0].Key.OK {
		t.Fatal("missing key should not be OK")
	}
}

func TestStatusFallsBackToListFields(t *testing.T) {
	env := newTestEnv(t)
	var server *httptest.Server
	server = newAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		if r.URL.Path == "/v1/workstations" {
			writeJSONResponse(w, http.StatusOK, map[string]any{"workstations": []map[string]any{{
				"id": "ws-1", "name": "solo", "state": "READY", "plan": "pro", "ipv4": "203.0.113.9",
				"ssh_alias": "custom-alias", "platform_generated_key": true,
			}}})
			return
		}
		writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": "not found"})
	})
	env.saveConfig(server.URL, "test-token")
	keyPath := env.keyPath("solo")
	env.writeFile(keyPath, []byte(testPrivateKey+"\n"), 0o600)
	block, err := sshconf.Build(sshconf.Block{
		ID: "ws-1", Alias: "custom-alias", HostName: "203.0.113.9", User: "agent", IdentityFile: keyPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	env.writeFile(env.sshConfigPath(), []byte(block), 0o600)

	code, stdout, stderr := env.run("status", "--json")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	report := decodeJSON[statusReport](t, stdout)
	ws := report.Workstations[0]
	if !ws.Configured || !ws.SSH.OK || !ws.Key.OK {
		t.Fatalf("list-only config should be considered configured: %+v", ws)
	}
	if ws.SSH.Alias != "custom-alias" {
		t.Fatalf("alias = %q", ws.SSH.Alias)
	}
	if !strings.Contains(strings.Join(ws.Issues, "; "), "connection details unavailable") {
		t.Fatalf("missing connection issue: %+v", ws.Issues)
	}

	code, stdout, _ = env.run("status")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(stdout, "local checks used list data") {
		t.Fatalf("missing connection note:\n%s", stdout)
	}
}
