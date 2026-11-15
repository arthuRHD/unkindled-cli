// Package name derives safe local filenames and SSH aliases from workstation
// names supplied by the control plane.
package name

import (
	"fmt"
	"strings"
	"unicode"
)

const maxSlug = 64

// Safe converts a workstation name into a filename-safe slug. Path separators
// and control characters are rejected rather than silently rewritten.
func Safe(workstation string) (string, error) {
	value := strings.TrimSpace(workstation)
	if value == "" {
		return "", fmt.Errorf("workstation name is empty")
	}
	if value == "." || value == ".." {
		return "", fmt.Errorf("workstation name %q is not a safe filename", workstation)
	}
	for _, r := range workstation {
		if r == '/' || r == '\\' {
			return "", fmt.Errorf("workstation name %q contains a path separator", workstation)
		}
		if unicode.IsControl(r) {
			return "", fmt.Errorf("workstation name %q contains a control character", workstation)
		}
	}

	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.':
			b.WriteRune(r)
			lastDash = false
		case r == '-':
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
		if b.Len() >= maxSlug {
			break
		}
	}
	slug := strings.Trim(b.String(), "-.")
	if slug == "" {
		return "", fmt.Errorf("workstation name %q has no usable characters for a filename", workstation)
	}
	return slug, nil
}

// Alias returns the default SSH host alias for a workstation.
func Alias(slug string) string {
	return "unkindled-" + slug
}

// ValidateAlias rejects aliases that could break an SSH config or command line.
func ValidateAlias(alias string) error {
	if alias == "" {
		return fmt.Errorf("SSH alias is empty")
	}
	for _, r := range alias {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '/' || r == '\\' || r == '"' || r == '\'' {
			return fmt.Errorf("SSH alias %q contains an unsafe character", alias)
		}
		if strings.ContainsRune("*?!,#", r) {
			return fmt.Errorf("SSH alias %q contains an SSH pattern character", alias)
		}
	}
	if strings.HasPrefix(alias, "-") {
		return fmt.Errorf("SSH alias %q must not start with a dash", alias)
	}
	return nil
}
