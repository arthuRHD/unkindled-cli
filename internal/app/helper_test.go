package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/arthuRHD/unkindled-cli/internal/api"
	"github.com/arthuRHD/unkindled-cli/internal/config"
)

type testEnv struct {
	t      *testing.T
	app    *App
	stdout bytes.Buffer
	stderr bytes.Buffer
	home   string
	xdg    string
	bin    string
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	root := t.TempDir()
	env := &testEnv{
		t:    t,
		home: filepath.Join(root, "home"),
		xdg:  filepath.Join(root, "xdg"),
		bin:  filepath.Join(root, "bin"),
	}
	for _, dir := range []string{env.home, env.xdg, env.bin} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", env.home)
	t.Setenv("XDG_CONFIG_HOME", env.xdg)
	t.Setenv("PATH", env.bin)
	t.Setenv("HERDR_LIST_JSON", "[]")
	t.Setenv("HERDR_LOG", filepath.Join(root, "herdr.log"))

	env.app = &App{
		Stdout:     &env.stdout,
		Stderr:     &env.stderr,
		Stdin:      strings.NewReader(""),
		HTTPClient: &http.Client{Timeout: 5 * time.Second},
		Sleep:      func(time.Duration) {},
		LookPath:   defaultLookPath,
	}
	return env
}

func defaultLookPath(file string) (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		candidate := filepath.Join(dir, file)
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", os.ErrNotExist
}

func (e *testEnv) saveConfig(apiURL, token string) {
	e.t.Helper()
	if _, err := config.Save(&config.Config{APIURL: apiURL, Token: token}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) run(args ...string) (int, string, string) {
	e.t.Helper()
	return e.runInput("", args...)
}

func (e *testEnv) runInput(input string, args ...string) (int, string, string) {
	e.t.Helper()
	e.stdout.Reset()
	e.stderr.Reset()
	e.app.Stdin = strings.NewReader(input)
	code := e.app.Run(context.Background(), args)
	return code, e.stdout.String(), e.stderr.String()
}

func (e *testEnv) configPath() string {
	return filepath.Join(e.xdg, "unkindled", "config.json")
}

func (e *testEnv) sshDir() string {
	return filepath.Join(e.home, ".ssh")
}

func (e *testEnv) sshConfigPath() string {
	return filepath.Join(e.sshDir(), "config")
}

func (e *testEnv) keyPath(slug string) string {
	return filepath.Join(e.sshDir(), "unkindled", slug)
}

func (e *testEnv) writeFile(path string, data []byte, mode os.FileMode) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		e.t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) readFile(path string) string {
	e.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatal(err)
	}
	return string(data)
}

func installFakeHerdr(t *testing.T, env *testEnv, listJSON, logPath string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(env.bin, "herdr"), []byte(fakeHerdrScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_LIST_JSON", listJSON)
	t.Setenv("HERDR_LOG", logPath)
}

const fakeHerdrScript = `#!/bin/sh
if [ "$1" = "machine" ] && [ "$2" = "list" ]; then
  printf '%s\n' "$HERDR_LIST_JSON"
  exit 0
fi
if [ "$1" = "machine" ] && [ "$2" = "add" ]; then
  printf '%s\n' "$*" >> "$HERDR_LOG"
  exit 0
fi
if [ "$1" = "machine" ] && [ "$2" = "remove" ]; then
  printf '%s\n' "$*" >> "$HERDR_LOG"
  exit 0
fi
exit 2
`

func newAPIServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func decodeJSON[T any](t *testing.T, data string) T {
	t.Helper()
	var value T
	if err := json.Unmarshal([]byte(data), &value); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, data)
	}
	return value
}

func writeJSONResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// newControlPlane returns a fake API that serves a workstation list, per-ID
// connection details, and the SSH key reveal endpoint.
func newControlPlane(t *testing.T, list []api.Workstation, connections map[string]api.Connection, revealedKey string, revealCalls *int) *httptest.Server {
	t.Helper()
	return newAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			writeJSONResponse(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workstations":
			writeJSONResponse(w, http.StatusOK, list)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/workstations/") && strings.HasSuffix(r.URL.Path, "/connection"):
			raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/workstations/"), "/connection")
			id, err := url.PathUnescape(strings.TrimSuffix(raw, "/"))
			if err != nil {
				http.Error(w, "bad id", http.StatusBadRequest)
				return
			}
			conn, ok := connections[id]
			if !ok {
				writeJSONResponse(w, http.StatusNotFound, map[string]any{"error": "workstation not found"})
				return
			}
			writeJSONResponse(w, http.StatusOK, conn)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/workstations/") && strings.HasSuffix(r.URL.Path, "/ssh-key/reveal"):
			if revealCalls != nil {
				(*revealCalls)++
			}
			writeJSONResponse(w, http.StatusOK, map[string]any{"private_key": revealedKey})
		default:
			http.NotFound(w, r)
		}
	})
}
