package diff

import (
	"strings"
	"testing"
)

func TestUnifiedChange(t *testing.T) {
	got := Unified("a\nb\nc\n", "a\nB\nc\n", "a/config", "b/config")
	want := "--- a/config\n+++ b/config\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"
	if got != want {
		t.Fatalf("Unified() =\n%q\nwant\n%q", got, want)
	}
}

func TestUnifiedAddAndRemove(t *testing.T) {
	got := Unified("", "a\n", "old", "new")
	if !strings.Contains(got, "--- old\n+++ new\n") || !strings.Contains(got, "+a") {
		t.Fatalf("unexpected diff:\n%s", got)
	}
	if got := Unified("a\nb\n", "a\n", "old", "new"); !strings.Contains(got, "-b") {
		t.Fatalf("unexpected removal diff:\n%s", got)
	}
}

func TestUnifiedIdentical(t *testing.T) {
	if got := Unified("a\nb\n", "a\nb\n", "old", "new"); got != "" {
		t.Fatalf("expected empty diff, got %q", got)
	}
	if got := Unified("", "", "old", "new"); got != "" {
		t.Fatalf("expected empty diff, got %q", got)
	}
}

func TestUnifiedContext(t *testing.T) {
	var oldLines, newLines []string
	for i := 0; i < 20; i++ {
		oldLines = append(oldLines, "line-"+string(rune('a'+i)))
		if i == 10 {
			newLines = append(newLines, "changed")
		} else {
			newLines = append(newLines, "line-"+string(rune('a'+i)))
		}
	}
	oldText := strings.Join(oldLines, "\n") + "\n"
	newText := strings.Join(newLines, "\n") + "\n"
	got := Unified(oldText, newText, "old", "new")
	if !strings.Contains(got, "@@ -8,7 +8,7 @@") {
		t.Fatalf("unexpected hunk header:\n%s", got)
	}
	if strings.Contains(got, "line-a") {
		t.Fatalf("diff contains too much context:\n%s", got)
	}
}
