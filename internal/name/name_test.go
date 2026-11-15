package name

import (
	"strings"
	"testing"
)

func TestSafe(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"dev box", "dev-box"},
		{"Dev Box", "dev-box"},
		{"api-prod-1", "api-prod-1"},
		{"my..box", "my..box"},
		{"  padded  ", "padded"},
		{"a/b", ""},
		{`a\b`, ""},
		{"a\nb", ""},
		{".", ""},
		{"..", ""},
		{"", ""},
		{"東京", ""},
	}
	for _, tt := range tests {
		got, err := Safe(tt.input)
		if tt.want == "" {
			if err == nil {
				t.Errorf("Safe(%q) = %q, want error", tt.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Safe(%q) error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Safe(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSafeTruncates(t *testing.T) {
	got, err := Safe(strings.Repeat("a", 200))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) > maxSlug {
		t.Fatalf("length = %d", len(got))
	}
}

func TestAlias(t *testing.T) {
	if got := Alias("dev-box"); got != "unkindled-dev-box" {
		t.Fatalf("Alias() = %q", got)
	}
}

func TestValidateAlias(t *testing.T) {
	valid := []string{"unkindled-dev-box", "host.example", "a_b+c@d", "host:2222"}
	for _, alias := range valid {
		if err := ValidateAlias(alias); err != nil {
			t.Errorf("ValidateAlias(%q) = %v", alias, err)
		}
	}
	invalid := []string{"", "bad alias", "-leading", "wild*card", "what?", "bang!", "comma,alias", "hash#alias", "quote\"alias", "slash/alias"}
	for _, alias := range invalid {
		if err := ValidateAlias(alias); err == nil {
			t.Errorf("ValidateAlias(%q) accepted unsafe alias", alias)
		}
	}
}
