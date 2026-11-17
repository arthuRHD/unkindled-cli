package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/arthuRHD/unkindled-cli/internal/api"
	"github.com/arthuRHD/unkindled-cli/internal/sshconf"
)

const testPrivateKey = "-----BEGIN OPENSSH PRIVATE KEY-----\nSECRET-KEY-MATERIAL\n-----END OPENSSH PRIVATE KEY-----"

func sshSyncFixture(t *testing.T, revealCalls *int) (*testEnv, *api.Workstation, *api.Connection) {
	t.Helper()
	env := newTestEnv(t)
	ws := api.Workstation{
		ID: "ws-1", Name: "dev box", State: "running", Plan: "pro", IPv4: "203.0.113.9",
	}
	conn := api.Connection{
		WorkstationID: "ws-1", IPv4: "203.0.113.9", User: "ubuntu",
		SSHHostAlias: "unkindled-dev box", PlatformGeneratedKey: true,
	}
	server := newControlPlane(t, []api.Workstation{ws}, map[string]api.Connection{"ws-1": conn}, testPrivateKey, revealCalls)
	env.saveConfig(server.URL, "test-token")
	return env, &ws, &conn
}

func TestSSHSyncAppliesAndIsIdempotent(t *testing.T) {
	revealCalls := 0
	env, _, _ := sshSyncFixture(t, &revealCalls)
	original := "# my ssh settings\nHost github.com\n  HostName github.com\n  User git\n"
	env.writeFile(env.sshConfigPath(), []byte(original), 0o644)

	code, stdout, stderr := env.run("ssh", "sync", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if revealCalls != 1 {
		t.Fatalf("reveal calls = %d, want 1", revealCalls)
	}
	if strings.Contains(stdout+stderr, "SECRET-KEY-MATERIAL") {
		t.Fatal("private key material was printed")
	}

	keyPath := env.keyPath("dev-box")
	key := env.readFile(keyPath)
	if !strings.Contains(key, "SECRET-KEY-MATERIAL") {
		t.Fatalf("key file content = %q", key)
	}
	keyInfo, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %04o, want 0600", keyInfo.Mode().Perm())
	}
	sshInfo, err := os.Stat(env.sshDir())
	if err != nil {
		t.Fatal(err)
	}
	if sshInfo.Mode().Perm() != 0o700 {
		t.Fatalf(".ssh mode = %04o, want 0700", sshInfo.Mode().Perm())
	}

	configText := env.readFile(env.sshConfigPath())
	for _, want := range []string{
		"# my ssh settings",
		"Host github.com",
		"  User git",
		"# unkindled:ws-1",
		"Host unkindled-dev-box",
		"  HostName 203.0.113.9",
		"  User ubuntu",
		"  IdentityFile " + keyPath,
		"  IdentitiesOnly yes",
		"# end unkindled:ws-1",
	} {
		if !strings.Contains(configText, want) {
			t.Errorf("SSH config missing %q:\n%s", want, configText)
		}
	}
	if strings.Index(configText, "# my ssh settings") > strings.Index(configText, "# unkindled:ws-1") {
		t.Error("existing SSH config order was not preserved")
	}
	configInfo, err := os.Stat(env.sshConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if configInfo.Mode().Perm() != 0o600 {
		t.Fatalf("SSH config mode = %04o, want 0600", configInfo.Mode().Perm())
	}

	before := configText
	code, stdout, stderr = env.run("ssh", "sync", "--yes")
	if code != 0 {
		t.Fatalf("second sync exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if revealCalls != 1 {
		t.Fatalf("second sync revealed the key again; calls = %d", revealCalls)
	}
	if !strings.Contains(stdout, "already up to date") {
		t.Fatalf("unexpected second sync output:\n%s", stdout)
	}
	if after := env.readFile(env.sshConfigPath()); after != before {
		t.Fatalf("second sync changed SSH config:\n%s", after)
	}
}

func TestSSHSyncDryRunMakesNoChanges(t *testing.T) {
	revealCalls := 0
	env, _, _ := sshSyncFixture(t, &revealCalls)

	code, stdout, stderr := env.run("ssh", "sync", "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if revealCalls != 0 {
		t.Fatalf("dry run revealed key %d time(s)", revealCalls)
	}
	if !strings.Contains(stdout, "Dry run") {
		t.Fatalf("missing dry run notice:\n%s", stdout)
	}
	if !strings.Contains(stdout, "--- ") || !strings.Contains(stdout, "+Host unkindled-dev-box") {
		t.Fatalf("missing diff:\n%s", stdout)
	}
	if _, err := os.Stat(env.sshConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("SSH config should not exist, err = %v", err)
	}
	if _, err := os.Stat(env.keyPath("dev-box")); !os.IsNotExist(err) {
		t.Fatalf("key should not exist, err = %v", err)
	}
}

func TestSSHSyncDeclined(t *testing.T) {
	revealCalls := 0
	env, _, _ := sshSyncFixture(t, &revealCalls)
	code, stdout, _ := env.runInput("n\n", "ssh", "sync")
	if code == 0 {
		t.Fatal("expected non-zero exit on decline")
	}
	if !strings.Contains(stdout, "Aborted") {
		t.Fatalf("unexpected output:\n%s", stdout)
	}
	if revealCalls != 0 {
		t.Fatalf("declined sync revealed key %d time(s)", revealCalls)
	}
	if _, err := os.Stat(env.sshConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("SSH config should not exist, err = %v", err)
	}
}

func TestSSHSyncUsesExistingKey(t *testing.T) {
	revealCalls := 0
	env, _, _ := sshSyncFixture(t, &revealCalls)
	keyPath := env.keyPath("dev-box")
	env.writeFile(keyPath, []byte(testPrivateKey+"\n"), 0o600)

	code, stdout, stderr := env.run("ssh", "sync", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if revealCalls != 0 {
		t.Fatalf("existing key was revealed; calls = %d", revealCalls)
	}
	if !strings.Contains(env.readFile(env.sshConfigPath()), "IdentityFile "+keyPath) {
		t.Fatal("SSH config was not written")
	}
}

func TestSSHSyncTightensPlatformKeyMode(t *testing.T) {
	revealCalls := 0
	env, _, _ := sshSyncFixture(t, &revealCalls)
	keyPath := env.keyPath("dev-box")
	env.writeFile(keyPath, []byte(testPrivateKey+"\n"), 0o644)

	code, _, stderr := env.run("ssh", "sync", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d\nstderr:\n%s", code, stderr)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode = %04o, want 0600", info.Mode().Perm())
	}
	if revealCalls != 0 {
		t.Fatal("key should not be revealed when it exists")
	}
}

func TestSSHSyncRejectsUnsafeWorkstationName(t *testing.T) {
	env := newTestEnv(t)
	ws := api.Workstation{ID: "ws-evil", Name: "../../escape", State: "running", IPv4: "203.0.113.9"}
	revealCalls := 0
	server := newControlPlane(t, []api.Workstation{ws}, map[string]api.Connection{}, testPrivateKey, &revealCalls)
	env.saveConfig(server.URL, "test-token")

	code, _, stderr := env.run("ssh", "sync", "--yes")
	if code == 0 {
		t.Fatal("expected failure for unsafe workstation name")
	}
	if !strings.Contains(stderr, "path separator") {
		t.Fatalf("unexpected stderr:\n%s", stderr)
	}
	if _, err := os.Stat(env.sshConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("SSH config should not exist, err = %v", err)
	}
	if _, err := os.Stat(env.sshDir()); !os.IsNotExist(err) {
		t.Fatalf(".ssh should not exist, err = %v", err)
	}
}

func TestSSHSyncUpdatesExistingManagedBlock(t *testing.T) {
	revealCalls := 0
	env, _, _ := sshSyncFixture(t, &revealCalls)
	keyPath := env.keyPath("dev-box")
	env.writeFile(keyPath, []byte(testPrivateKey+"\n"), 0o600)
	block, err := sshconf.Build(sshconf.Block{
		ID: "ws-1", Alias: "old-alias", HostName: "198.51.100.1", User: "root", IdentityFile: "/old/key",
	})
	if err != nil {
		t.Fatal(err)
	}
	original := "# top\n\n" + block + "\n# bottom\n"
	env.writeFile(env.sshConfigPath(), []byte(original), 0o600)

	code, _, stderr := env.run("ssh", "sync", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d\nstderr:\n%s", code, stderr)
	}
	text := env.readFile(env.sshConfigPath())
	if strings.Contains(text, "old-alias") || strings.Contains(text, "/old/key") {
		t.Fatalf("old block was not replaced:\n%s", text)
	}
	if !strings.Contains(text, "Host unkindled-dev-box") || !strings.Contains(text, "# top") || !strings.Contains(text, "# bottom") {
		t.Fatalf("unexpected config:\n%s", text)
	}
	if strings.Count(text, "# unkindled:ws-1") != 1 {
		t.Fatalf("expected exactly one managed block:\n%s", text)
	}
}

func TestSSHSyncNonPlatformKeyMissing(t *testing.T) {
	env := newTestEnv(t)
	ws := api.Workstation{ID: "ws-2", Name: "custom", IPv4: "198.51.100.2"}
	conn := api.Connection{IPv4: "198.51.100.2", User: "deploy", PlatformGeneratedKey: false}
	server := newControlPlane(t, []api.Workstation{ws}, map[string]api.Connection{"ws-2": conn}, testPrivateKey, nil)
	env.saveConfig(server.URL, "test-token")

	code, _, stderr := env.run("ssh", "sync", "--yes")
	if code == 0 {
		t.Fatal("expected failure for missing externally managed key")
	}
	if !strings.Contains(stderr, "identity file") {
		t.Fatalf("unexpected stderr:\n%s", stderr)
	}
}

func TestSSHSyncUsesExternalIdentityFile(t *testing.T) {
	env := newTestEnv(t)
	ws := api.Workstation{ID: "ws-2", Name: "custom", IPv4: "198.51.100.2"}
	conn := api.Connection{
		IPv4: "198.51.100.2", User: "deploy",
		IdentityFile: "~/.ssh/id_ed25519", PlatformGeneratedKey: false,
	}
	server := newControlPlane(t, []api.Workstation{ws}, map[string]api.Connection{"ws-2": conn}, testPrivateKey, nil)
	env.saveConfig(server.URL, "test-token")
	externalKey := filepath.Join(env.home, ".ssh", "id_ed25519")
	env.writeFile(externalKey, []byte("external key\n"), 0o600)

	code, stdout, stderr := env.run("ssh", "sync", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	text := env.readFile(env.sshConfigPath())
	if !strings.Contains(text, "IdentityFile "+externalKey) {
		t.Fatalf("external identity not used:\n%s", text)
	}
	if strings.Contains(text, "unkindled/custom") {
		t.Fatalf("managed key path used for external key:\n%s", text)
	}
}

func TestSSHSyncRecognizesEquivalentUnmanagedBlock(t *testing.T) {
	revealCalls := 0
	env, _, _ := sshSyncFixture(t, &revealCalls)
	keyPath := env.keyPath("dev-box")
	env.writeFile(keyPath, []byte(testPrivateKey), 0o600)
	original := "Host unkindled-dev-box\n" +
		"  HostName 203.0.113.9\n" +
		"  User ubuntu\n" +
		"  IdentityFile " + keyPath + "\n" +
		"  IdentitiesOnly yes\n"
	env.writeFile(env.sshConfigPath(), []byte(original), 0o600)

	code, stdout, stderr := env.run("status")
	if code != 0 {
		t.Fatalf("status exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "All workstations are configured locally.") {
		t.Fatalf("equivalent unmanaged block not detected:\n%s", stdout)
	}
	if revealCalls != 0 {
		t.Fatalf("status revealed a key")
	}

	code, stdout, stderr = env.run("ssh", "sync", "--dry-run")
	if code != 0 {
		t.Fatalf("sync exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "already up to date") {
		t.Fatalf("unmanaged equivalent block was not treated as configured:\n%s", stdout)
	}
	if after := env.readFile(env.sshConfigPath()); after != original {
		t.Fatalf("sync changed the unmanaged block:\n%s", after)
	}
}

func TestSSHSyncUsesServerIdentityPath(t *testing.T) {
	revealCalls := 0
	env := newTestEnv(t)
	ws := api.Workstation{ID: "ws-1", Name: "dev box", State: "running", Plan: "pro", IPv4: "203.0.113.9"}
	conn := api.Connection{
		WorkstationID: "ws-1", IPv4: "203.0.113.9", User: "ubuntu",
		SSHHostAlias: "unkindled-dev-box", PlatformGeneratedKey: true,
		IdentityFile: "~/.ssh/unkindled-dev-box",
	}
	server := newControlPlane(t, []api.Workstation{ws}, map[string]api.Connection{"ws-1": conn}, testPrivateKey, &revealCalls)
	env.saveConfig(server.URL, "test-token")
	keyPath := filepath.Join(env.home, ".ssh", "unkindled-dev-box")
	env.writeFile(keyPath, []byte(testPrivateKey), 0o600)

	code, stdout, stderr := env.run("ssh", "sync", "--dry-run")
	if code != 0 {
		t.Fatalf("sync exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if revealCalls != 0 {
		t.Fatalf("existing server-side identity path was not detected; reveal calls = %d", revealCalls)
	}
	if strings.Contains(stdout, "write platform-generated private key") {
		t.Fatalf("planned a second key instead of using the server identity path:\n%s", stdout)
	}
}

func TestSSHSyncPrunesDeletedManagedEntries(t *testing.T) {
	revealCalls := 0
	env, _, _ := sshSyncFixture(t, &revealCalls)
	activeKey := env.keyPath("dev-box")
	env.writeFile(activeKey, []byte(testPrivateKey), 0o600)
	staleKey := env.keyPath("old-box")
	env.writeFile(staleKey, []byte(testPrivateKey), 0o600)
	configText := "Host personal\n  HostName 198.51.100.7\n  User me\n\n# unkindled:ws-old\nHost old-box\n  HostName 203.0.113.50\n  User ubuntu\n  IdentityFile " + staleKey + "\n  IdentitiesOnly yes\n# end unkindled:ws-old\n"
	env.writeFile(env.sshConfigPath(), []byte(configText), 0o600)

	code, stdout, stderr := env.run("ssh", "sync", "--prune", "--yes")
	if code != 0 {
		t.Fatalf("exit = %d\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if revealCalls != 0 {
		t.Fatalf("sync revealed a key; calls = %d", revealCalls)
	}
	after := env.readFile(env.sshConfigPath())
	if strings.Contains(after, "ws-old") || strings.Contains(after, "old-box") {
		t.Fatalf("stale managed block was not pruned:\n%s", after)
	}
	if !strings.Contains(after, "unkindled:ws-1") {
		t.Fatalf("active managed block was not written:\n%s", after)
	}
	if !strings.Contains(after, "Host personal") || !strings.Contains(after, "198.51.100.7") {
		t.Fatalf("unrelated SSH config was removed:\n%s", after)
	}
	if _, err := os.Stat(staleKey); !os.IsNotExist(err) {
		t.Fatalf("stale key still exists: %v", err)
	}
	if _, err := os.Stat(activeKey); err != nil {
		t.Fatalf("active key was removed: %v", err)
	}
	if !strings.Contains(stdout, "remove managed SSH config block and key") {
		t.Fatalf("prune output missing:\n%s", stdout)
	}
}
