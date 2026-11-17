package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arthuRHD/unkindled-cli/internal/api"
)

func lifecycleFixture(t *testing.T) (*testEnv, *map[string]bool, *map[string]map[string]any) {
	t.Helper()
	env := newTestEnv(t)
	calls := map[string]bool{}
	bodies := map[string]map[string]any{}
	ws := api.Workstation{ID: "ws-1", Name: "dev-box", State: "READY", Plan: "solo", IPv4: "203.0.113.9"}
	templates := []api.Template{{ID: "tpl-1", Name: "Starter", Slug: "starter", Plan: "solo", Description: "A starter box"}}
	server := newAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workstations":
			writeJSONResponse(w, http.StatusOK, map[string]any{"workstations": []api.Workstation{ws}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workstations/ws-1/stop":
			calls["stop"] = true
			writeJSONResponse(w, http.StatusAccepted, map[string]any{"status": "requested"})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/workstations/ws-1":
			if r.URL.Query().Get("confirm") != "true" {
				writeJSONResponse(w, http.StatusBadRequest, map[string]any{"error": "confirmation required"})
				return
			}
			calls["kill"] = true
			writeJSONResponse(w, http.StatusAccepted, map[string]any{"status": "requested"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/templates":
			writeJSONResponse(w, http.StatusOK, map[string]any{"templates": templates})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/templates/tpl-1/launch":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			bodies["launch"] = body
			calls["launch"] = true
			writeJSONResponse(w, http.StatusAccepted, map[string]any{"workstation": api.Workstation{
				ID: "ws-new", Name: "new-box", State: "PROVISIONING", Plan: "solo",
			}})
		default:
			http.NotFound(w, r)
		}
	})
	env.saveConfig(server.URL, "test-token")
	return env, &calls, &bodies
}

func TestStopAndKillCommands(t *testing.T) {
	env, calls, _ := lifecycleFixture(t)

	code, stdout, stderr := env.run("stop", "dev-box")
	if code != 0 {
		t.Fatalf("stop exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !(*calls)["stop"] {
		t.Fatal("stop endpoint was not called")
	}
	if !strings.Contains(stdout, "Stop requested for dev-box") {
		t.Fatalf("unexpected stop output:\n%s", stdout)
	}

	code, stdout, stderr = env.runInput("n\n", "kill", "ws-1")
	if code == 0 || (*calls)["kill"] {
		t.Fatalf("kill should abort without confirmation: code=%d calls=%v\n%s\n%s", code, *calls, stdout, stderr)
	}
	code, stdout, stderr = env.run("kill", "dev-box", "--yes")
	if code != 0 {
		t.Fatalf("kill exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !(*calls)["kill"] {
		t.Fatal("kill endpoint was not called")
	}
	if !strings.Contains(stdout, "Deletion requested for dev-box") {
		t.Fatalf("unexpected kill output:\n%s", stdout)
	}
}

func TestTemplatesAndCreateFromTemplate(t *testing.T) {
	env, calls, bodies := lifecycleFixture(t)
	keyPath := filepath.Join(env.home, "id_ed25519.pub")
	if err := os.WriteFile(keyPath, []byte("ssh-ed25519 AAAATEST user@example\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := env.run("templates")
	if code != 0 {
		t.Fatalf("templates exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Starter") || !strings.Contains(stdout, "starter") {
		t.Fatalf("template list missing entry:\n%s", stdout)
	}

	code, stdout, stderr = env.run("create", "--template", "Starter", "--name", "new-box", "--ssh-key", keyPath)
	if code != 0 {
		t.Fatalf("create exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !(*calls)["launch"] {
		t.Fatal("template launch endpoint was not called")
	}
	body := (*bodies)["launch"]
	if body["name"] != "new-box" {
		t.Fatalf("launch name = %v", body["name"])
	}
	if !strings.Contains(body["ssh_public_key"].(string), "ssh-ed25519 AAAATEST") {
		t.Fatalf("launch public key = %v", body["ssh_public_key"])
	}
	if !strings.Contains(stdout, "new-box") {
		t.Fatalf("unexpected create output:\n%s", stdout)
	}
}
