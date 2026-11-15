package app

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/arthuRHD/unkindled-cli/internal/config"
)

func TestLoginDeviceFlow(t *testing.T) {
	env := newTestEnv(t)
	pollCalls := 0
	gotDeviceCode := ""
	server := newAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/github/device/start":
			if r.Method != http.MethodPost {
				t.Errorf("start method = %s", r.Method)
			}
			writeJSONResponse(w, http.StatusOK, map[string]any{
				"device_code":      "dev-123",
				"user_code":        "ABCD-1234",
				"verification_uri": "https://github.com/login/device",
				"expires_in":       900,
				"interval":         1,
			})
		case "/v1/auth/github/device/poll":
			pollCalls++
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode poll body: %v", err)
			}
			gotDeviceCode = body["device_code"]
			if pollCalls == 1 {
				writeJSONResponse(w, http.StatusOK, map[string]any{"status": "pending"})
				return
			}
			writeJSONResponse(w, http.StatusOK, map[string]any{
				"status": "complete",
				"token":  "tok_secret_value",
				"user":   map[string]any{"login": "octocat"},
			})
		default:
			http.NotFound(w, r)
		}
	})

	code, stdout, stderr := env.run("login", "--api", server.URL, "--json")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if pollCalls != 2 {
		t.Fatalf("poll calls = %d, want 2", pollCalls)
	}
	if gotDeviceCode != "dev-123" {
		t.Fatalf("poll device_code = %q", gotDeviceCode)
	}
	if strings.Contains(stdout+stderr, "tok_secret_value") {
		t.Fatal("token was printed")
	}
	if !strings.Contains(stdout, "ABCD-1234") || !strings.Contains(stdout, "https://github.com/login/device") {
		t.Fatalf("missing device instructions:\n%s", stdout)
	}
	if !strings.Contains(stdout, `"status": "pending"`) || !strings.Contains(stdout, `"status": "complete"`) {
		t.Fatalf("missing JSON events:\n%s", stdout)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "tok_secret_value" || cfg.APIURL != server.URL {
		t.Fatalf("stored config = %+v", cfg)
	}
	if strings.TrimSpace(env.readFile(env.configPath())) == "" {
		t.Fatal("config file missing")
	}
	info, err := os.Stat(env.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestLoginSlowDown(t *testing.T) {
	env := newTestEnv(t)
	polls := 0
	server := newAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/github/device/start":
			writeJSONResponse(w, http.StatusOK, map[string]any{
				"device_code": "dev", "user_code": "CODE", "verification_uri": "https://example.test/device",
				"expires_in": 900, "interval": 1,
			})
		case "/v1/auth/github/device/poll":
			polls++
			switch polls {
			case 1:
				writeJSONResponse(w, http.StatusOK, map[string]any{"status": "slow_down"})
			case 2:
				writeJSONResponse(w, http.StatusOK, map[string]any{"status": "pending"})
			default:
				writeJSONResponse(w, http.StatusOK, map[string]any{"status": "complete", "token": "tok"})
			}
		}
	})
	code, stdout, stderr := env.run("login", "--api", server.URL)
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if polls != 3 {
		t.Fatalf("polls = %d, want 3", polls)
	}
	if !strings.Contains(stdout, "Logged in") {
		t.Fatalf("unexpected output:\n%s", stdout)
	}
}

func TestLoginTerminalStatuses(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		body     map[string]any
		wantCode int
		wantErr  string
	}{
		{name: "denied", status: "denied", body: map[string]any{"status": "denied"}, wantCode: 1, wantErr: "denied"},
		{name: "expired", status: "expired", body: map[string]any{"status": "expired"}, wantCode: 1, wantErr: "expired"},
		{name: "http denied", status: "http", body: map[string]any{"status": "access_denied"}, wantCode: 1, wantErr: "denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t)
			server := newAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/auth/github/device/start":
					writeJSONResponse(w, http.StatusOK, map[string]any{
						"device_code": "dev", "user_code": "CODE", "verification_uri": "https://example.test/device",
						"expires_in": 900, "interval": 1,
					})
				case "/v1/auth/github/device/poll":
					if tt.status == "http" {
						writeJSONResponse(w, http.StatusBadRequest, tt.body)
						return
					}
					writeJSONResponse(w, http.StatusOK, tt.body)
				}
			})
			code, _, stderr := env.run("login", "--api", server.URL)
			if code != tt.wantCode {
				t.Fatalf("exit = %d, want %d\nstderr:\n%s", code, tt.wantCode, stderr)
			}
			if !strings.Contains(strings.ToLower(stderr), tt.wantErr) {
				t.Fatalf("stderr missing %q:\n%s", tt.wantErr, stderr)
			}
			if _, err := os.Stat(env.configPath()); !os.IsNotExist(err) {
				t.Fatalf("config should not exist, stat err = %v", err)
			}
		})
	}
}

func TestLoginNetworkError(t *testing.T) {
	env := newTestEnv(t)
	server := newAPIServer(t, func(w http.ResponseWriter, r *http.Request) {})
	server.Close()

	code, _, stderr := env.run("login", "--api", server.URL)
	if code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(stderr, "could not start GitHub device login") {
		t.Fatalf("unexpected stderr:\n%s", stderr)
	}
}

func TestLoginTokenFlag(t *testing.T) {
	env := newTestEnv(t)
	code, stdout, stderr := env.run("login", "--api", "https://api.example.test/", "--token", "tok_secret", "--json")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "tok_secret") {
		t.Fatal("token was printed")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "tok_secret" || cfg.APIURL != "https://api.example.test" {
		t.Fatalf("config = %+v", cfg)
	}
	if !strings.Contains(stdout, `"status": "complete"`) {
		t.Fatalf("unexpected output:\n%s", stdout)
	}
}

func TestLoginNoTokenPrintOnFailure(t *testing.T) {
	env := newTestEnv(t)
	server := newAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/github/device/start":
			writeJSONResponse(w, http.StatusOK, map[string]any{
				"device_code": "dev", "user_code": "CODE", "verification_uri": "https://example.test/device",
				"expires_in": 900, "interval": 1,
			})
		case "/v1/auth/github/device/poll":
			writeJSONResponse(w, http.StatusOK, map[string]any{"status": "complete", "token": ""})
		}
	})
	code, stdout, stderr := env.run("login", "--api", server.URL)
	if code == 0 {
		t.Fatal("expected failure")
	}
	if strings.Contains(stdout+stderr, "tok_") {
		t.Fatal("unexpected token-like output")
	}
	if !strings.Contains(stderr, "no token") {
		t.Fatalf("unexpected stderr:\n%s", stderr)
	}
}

func TestLoginUsesServerPollInterval(t *testing.T) {
	env := newTestEnv(t)
	var sleeps []time.Duration
	env.app.Sleep = func(duration time.Duration) { sleeps = append(sleeps, duration) }
	polls := 0
	server := newAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/github/device/start":
			writeJSONResponse(w, http.StatusOK, map[string]any{
				"device_code": "dev", "user_code": "CODE", "verification_uri": "https://example.test/device",
				"expires_in": 900, "interval": 1,
			})
		case "/v1/auth/github/device/poll":
			polls++
			if polls == 1 {
				writeJSONResponse(w, http.StatusOK, map[string]any{"status": "pending", "interval": 7})
				return
			}
			writeJSONResponse(w, http.StatusOK, map[string]any{"status": "complete", "token": "tok"})
		}
	})
	code, _, stderr := env.run("login", "--api", server.URL)
	if code != 0 {
		t.Fatalf("exit = %d\nstderr:\n%s", code, stderr)
	}
	if len(sleeps) != 1 || sleeps[0] != 7*time.Second {
		t.Fatalf("sleeps = %v, want [7s]", sleeps)
	}
}

func TestLoginMapsAPIErrorEnvelope(t *testing.T) {
	env := newTestEnv(t)
	server := newAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/github/device/start":
			writeJSONResponse(w, http.StatusOK, map[string]any{
				"device_code": "dev", "user_code": "CODE", "verification_uri": "https://example.test/device",
				"expires_in": 900, "interval": 1,
			})
		case "/v1/auth/github/device/poll":
			writeJSONResponse(w, http.StatusForbidden, map[string]any{
				"error": map[string]string{"code": "device_flow_denied", "message": "GitHub sign-in was denied"},
			})
		}
	})
	code, _, stderr := env.run("login", "--api", server.URL)
	if code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(stderr, "denied") || !strings.Contains(stderr, "unkindled login") {
		t.Fatalf("unexpected stderr:\n%s", stderr)
	}
}

func TestLoginStartErrorMessage(t *testing.T) {
	env := newTestEnv(t)
	server := newAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{
			"error": map[string]string{"code": "github_not_configured", "message": "GitHub device sign-in is not configured on this deployment"},
		})
	})
	code, _, stderr := env.run("login", "--api", server.URL)
	if code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(stderr, "not configured") {
		t.Fatalf("server message missing from stderr:\n%s", stderr)
	}
}
