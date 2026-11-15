package app

import (
	"os"
	"strings"
	"testing"
)

func TestDispatchHelpVersionAndUnknown(t *testing.T) {
	env := newTestEnv(t)

	code, stdout, _ := env.run()
	if code != 0 || !strings.Contains(stdout, "Usage:") {
		t.Fatalf("no-arg run: code=%d stdout=%q", code, stdout)
	}
	code, stdout, _ = env.run("help")
	if code != 0 || !strings.Contains(stdout, "ssh sync") {
		t.Fatalf("help: code=%d stdout=%q", code, stdout)
	}
	code, stdout, _ = env.run("version")
	if code != 0 || !strings.Contains(stdout, Version) {
		t.Fatalf("version: code=%d stdout=%q", code, stdout)
	}
	code, _, stderr := env.run("nope")
	if code != 2 || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("unknown: code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = env.run("ssh")
	if code != 2 || !strings.Contains(stderr, "usage: unkindled ssh sync") {
		t.Fatalf("ssh usage: code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = env.run("ssh", "nothing")
	if code != 2 || !strings.Contains(stderr, "unknown ssh subcommand") {
		t.Fatalf("ssh unknown: code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = env.run("herdr")
	if code != 2 || !strings.Contains(stderr, "usage: unkindled herdr sync") {
		t.Fatalf("herdr usage: code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = env.run("status", "extra")
	if code != 2 || !strings.Contains(stderr, "unexpected argument") {
		t.Fatalf("status extra: code=%d stderr=%q", code, stderr)
	}
}

func TestLogout(t *testing.T) {
	env := newTestEnv(t)
	env.saveConfig("https://api.example.test", "tok_secret")
	if _, err := os.Stat(env.configPath()); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := env.run("logout")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "tok_secret") {
		t.Fatal("token was printed")
	}
	if !strings.Contains(stdout, "not revoked") {
		t.Fatalf("missing revocation note:\n%s", stdout)
	}
	if _, err := os.Stat(env.configPath()); !os.IsNotExist(err) {
		t.Fatalf("config still exists, err = %v", err)
	}

	code, stdout, _ = env.run("logout")
	if code != 0 || !strings.Contains(stdout, "Not logged in") {
		t.Fatalf("second logout: code=%d stdout=%q", code, stdout)
	}
}

func TestStatusNotLoggedIn(t *testing.T) {
	env := newTestEnv(t)
	code, _, stderr := env.run("status")
	if code == 0 {
		t.Fatal("expected failure")
	}
	if !strings.Contains(stderr, "unkindled login") {
		t.Fatalf("missing login hint:\n%s", stderr)
	}
}
