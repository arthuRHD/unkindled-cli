package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathHonorsXDG(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	xdg := filepath.Join(root, "xdg")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)

	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join(xdg, "unkindled") {
		t.Fatalf("Dir() = %q", dir)
	}
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(xdg, "unkindled", "config.json") {
		t.Fatalf("Path() = %q", path)
	}
}

func TestPathFallsBackToHome(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(home, ".config", "unkindled", "config.json") {
		t.Fatalf("Path() = %q", path)
	}
}

func TestPathIgnoresRelativeXDG(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "relative/path")

	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, home) {
		t.Fatalf("relative XDG was used: %q", path)
	}
}

func TestSaveLoadDelete(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))

	cfg := &Config{APIURL: "https://api.example.test", Token: "secret-token", User: map[string]any{"login": "octocat"}}
	path, err := Save(cfg)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %04o", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("config dir mode = %04o", dirInfo.Mode().Perm())
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.APIURL != cfg.APIURL || loaded.Token != cfg.Token || loaded.User["login"] != "octocat" {
		t.Fatalf("loaded = %+v", loaded)
	}

	cfg.Token = "rotated"
	if _, err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Token != "rotated" {
		t.Fatalf("token = %q", loaded.Token)
	}

	removedPath, removed, err := Delete()
	if err != nil {
		t.Fatal(err)
	}
	if !removed || removedPath != path {
		t.Fatalf("Delete() = %q, %v", removedPath, removed)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("config still exists, err = %v", err)
	}
	if _, err := Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load after delete = %v", err)
	}
	_, removed, err = Delete()
	if err != nil || removed {
		t.Fatalf("second Delete() = %v, %v", removed, err)
	}
}

func TestLoadDefaultAPIURL(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte(`{"token":"abc"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIURL != DefaultAPIURL {
		t.Fatalf("APIURL = %q", cfg.APIURL)
	}
}

func TestLoadMalformed(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("LoadFile error = %v", err)
	}
}
