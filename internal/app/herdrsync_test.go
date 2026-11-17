package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arthuRHD/unkindled-cli/internal/api"
)

func herdrFixture(t *testing.T) (*testEnv, string) {
	t.Helper()
	env := newTestEnv(t)
	ws := api.Workstation{ID: "ws-1", Name: "dev box", State: "running", Plan: "pro", IPv4: "203.0.113.9"}
	conn := api.Connection{IPv4: "203.0.113.9", User: "ubuntu", SSHHostAlias: "unkindled-dev box"}
	server := newControlPlane(t, []api.Workstation{ws}, map[string]api.Connection{"ws-1": conn}, testPrivateKey, nil)
	env.saveConfig(server.URL, "test-token")
	return env, filepath.Join(t.TempDir(), "herdr.log")
}

func TestHerdrSyncAddsMissingMachine(t *testing.T) {
	env, logPath := herdrFixture(t)
	installFakeHerdr(t, env, "[]", logPath)

	code, stdout, stderr := env.run("herdr", "sync", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	log := readLog(logPath)
	if !strings.Contains(log, "machine add unkindled-dev-box --label dev box --remote-session default") {
		t.Fatalf("unexpected herdr log:\n%s", log)
	}
	if !strings.Contains(stdout, "Registered dev box") {
		t.Fatalf("unexpected output:\n%s", stdout)
	}

	t.Setenv("HERDR_LIST_JSON", `[{"id":"1","label":"dev box","target":"unkindled-dev-box","session":"default"}]`)
	code, stdout, stderr = env.run("herdr", "sync", "--yes")
	if code != 0 {
		t.Fatalf("second exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "already up to date") {
		t.Fatalf("unexpected second output:\n%s", stdout)
	}
	if logAfter := readLog(logPath); logAfter != log {
		t.Fatalf("existing machine was added again:\n%s", logAfter)
	}
}

func TestHerdrSyncPrintAndDryRun(t *testing.T) {
	for _, args := range [][]string{
		{"herdr", "sync", "--print"},
		{"herdr", "sync", "--dry-run"},
	} {
		t.Run(args[2], func(t *testing.T) {
			env, logPath := herdrFixture(t)
			installFakeHerdr(t, env, "[]", logPath)
			code, stdout, stderr := env.run(args...)
			if code != 0 {
				t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
			}
			if !strings.Contains(stdout, "herdr machine add unkindled-dev-box --label 'dev box' --remote-session default") {
				t.Fatalf("unexpected output:\n%s", stdout)
			}
			if log := readLog(logPath); log != "" {
				t.Fatalf("herdr was invoked during %s:\n%s", args[2], log)
			}
		})
	}
}

func TestHerdrSyncDeclined(t *testing.T) {
	env, logPath := herdrFixture(t)
	installFakeHerdr(t, env, "[]", logPath)
	code, stdout, _ := env.runInput("n\n", "herdr", "sync")
	if code == 0 {
		t.Fatal("expected non-zero exit on decline")
	}
	if !strings.Contains(stdout, "Aborted") {
		t.Fatalf("unexpected output:\n%s", stdout)
	}
	if log := readLog(logPath); log != "" {
		t.Fatalf("herdr was invoked after decline:\n%s", log)
	}
}

func TestHerdrMissing(t *testing.T) {
	env, _ := herdrFixture(t)

	code, stdout, stderr := env.run("herdr", "sync", "--yes")
	if code == 0 {
		t.Fatal("expected non-zero exit when herdr is missing and sync was requested")
	}
	if !strings.Contains(stderr, "herdr is not installed") || !strings.Contains(stderr, "https://herdr.dev") {
		t.Fatalf("missing install hint:\nstderr:\n%s", stderr)
	}
	if stdout != "" {
		t.Fatalf("unexpected stdout:\n%s", stdout)
	}

	code, stdout, stderr = env.run("herdr", "sync", "--print")
	if code != 0 {
		t.Fatalf("print exit = %d\nstderr:\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "herdr machine add unkindled-dev-box") {
		t.Fatalf("print did not show command:\n%s", stdout)
	}
	if !strings.Contains(stderr, "herdr is not installed") {
		t.Fatalf("print missing install hint:\n%s", stderr)
	}
}

func readLog(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func TestHerdrSyncPrunesStaleMachines(t *testing.T) {
	env, logPath := herdrFixture(t)
	list := `[{"id":"active","label":"dev box","target":"unkindled-dev-box","session":"default"},{"id":"stale","label":"old box","target":"unkindled-old-box","session":"default"},{"id":"mine","label":"personal","target":"myserver","session":"default"}]`
	installFakeHerdr(t, env, list, logPath)

	code, stdout, stderr := env.run("herdr", "sync", "--prune", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Removed stale Herdr machine") {
		t.Fatalf("unexpected output:\n%s", stdout)
	}
	log := readLog(logPath)
	if !strings.Contains(log, "machine remove stale") {
		t.Fatalf("stale machine was not removed:\n%s", log)
	}
	if strings.Contains(log, "machine add") {
		t.Fatalf("active machine was re-added:\n%s", log)
	}
	if strings.Contains(log, "machine remove mine") {
		t.Fatalf("unrelated machine was removed:\n%s", log)
	}
}

func TestHerdrSyncPrunePrintDoesNotInvoke(t *testing.T) {
	env, logPath := herdrFixture(t)
	list := `[{"id":"active","label":"dev box","target":"unkindled-dev-box","session":"default"},{"id":"stale","label":"old box","target":"unkindled-old-box","session":"default"}]`
	installFakeHerdr(t, env, list, logPath)

	code, stdout, stderr := env.run("herdr", "sync", "--prune", "--print")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "herdr machine remove stale") {
		t.Fatalf("prune command missing:\n%s", stdout)
	}
	if log := readLog(logPath); log != "" {
		t.Fatalf("herdr was invoked during --print:\n%s", log)
	}
}

func TestHerdrSyncPruneWithNoWorkstations(t *testing.T) {
	env := newTestEnv(t)
	server := newControlPlane(t, []api.Workstation{}, map[string]api.Connection{}, testPrivateKey, nil)
	env.saveConfig(server.URL, "test-token")
	logPath := filepath.Join(t.TempDir(), "herdr.log")
	list := `[{"id":"stale","label":"old box","target":"unkindled-old-box","session":"default"},{"id":"mine","label":"personal","target":"myserver","session":"default"}]`
	installFakeHerdr(t, env, list, logPath)

	code, stdout, stderr := env.run("herdr", "sync", "--prune", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	log := readLog(logPath)
	if !strings.Contains(log, "machine remove stale") {
		t.Fatalf("stale machine was not removed:\n%s", log)
	}
	if strings.Contains(log, "machine remove mine") {
		t.Fatalf("unrelated machine was removed:\n%s", log)
	}
}
