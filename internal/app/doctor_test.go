package app

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/arthuRHD/unkindled-cli/internal/api"
	"github.com/arthuRHD/unkindled-cli/internal/sshconf"
)

func TestDoctorPassesConfiguredSetup(t *testing.T) {
	env := statusFixture(t, `[{"label":"dev box"},{"label":"prod"}]`)
	code, stdout, stderr := env.run("doctor")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	for _, want := range []string{"[PASS]", "config", "api", "ssh key ws-1", "herdr", "Everything looks good."} {
		if !strings.Contains(stdout, want) {
			t.Errorf("doctor output missing %q:\n%s", want, stdout)
		}
	}
}

func TestDoctorMissingLogin(t *testing.T) {
	env := newTestEnv(t)
	code, stdout, _ := env.run("doctor")
	if code == 0 {
		t.Fatal("expected failure without config")
	}
	if !strings.Contains(stdout, "[FAIL]") || !strings.Contains(stdout, "not found") {
		t.Fatalf("unexpected output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Next: unkindled login") {
		t.Fatalf("missing next command:\n%s", stdout)
	}
}

func TestDoctorBadKeyPermissions(t *testing.T) {
	env := statusFixture(t, `[{"label":"dev box"}]`)
	keyPath := env.keyPath("dev-box")
	if err := os.Chmod(keyPath, 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := env.run("doctor")
	if code == 0 {
		t.Fatal("expected failure for bad key permissions")
	}
	if !strings.Contains(stdout, "expected 0600") {
		t.Fatalf("unexpected output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Next: unkindled ssh sync") {
		t.Fatalf("missing next command:\n%s", stdout)
	}
}

func TestDoctorConfigPermissions(t *testing.T) {
	env := statusFixture(t, `[{"label":"dev box"}]`)
	if err := os.Chmod(env.configPath(), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, _ := env.run("doctor")
	if code == 0 {
		t.Fatal("expected failure for permissive config")
	}
	if !strings.Contains(stdout, "mode 0644") {
		t.Fatalf("unexpected output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Next: chmod 600") {
		t.Fatalf("missing chmod suggestion:\n%s", stdout)
	}
}

func TestDoctorWithoutHerdr(t *testing.T) {
	env := newTestEnv(t)
	ws := api.Workstation{ID: "ws-1", Name: "solo", State: "running", IPv4: "203.0.113.9"}
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

	code, stdout, _ := env.run("doctor")
	if code != 0 {
		t.Fatalf("herdr absence should not fail doctor:\n%s", stdout)
	}
	if !strings.Contains(stdout, "[WARN]") || !strings.Contains(stdout, "herdr") {
		t.Fatalf("missing herdr warning:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Next: install herdr") {
		t.Fatalf("missing herdr install suggestion:\n%s", stdout)
	}
}

func TestDoctorAPIUnreachable(t *testing.T) {
	env := newTestEnv(t)
	server := newAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	})
	env.saveConfig(server.URL, "test-token")
	code, stdout, _ := env.run("doctor")
	if code == 0 {
		t.Fatal("expected failure for unavailable API")
	}
	if !strings.Contains(stdout, "HTTP 503") && !strings.Contains(stdout, "control plane") {
		t.Fatalf("unexpected output:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Next: check your network") {
		t.Fatalf("missing next command:\n%s", stdout)
	}
}
