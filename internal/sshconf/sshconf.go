// Package sshconf safely edits managed blocks in an OpenSSH client config.
package sshconf

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Block describes one unkindled-managed SSH host entry.
type Block struct {
	ID           string
	Alias        string
	HostName     string
	User         string
	Port         int
	IdentityFile string
}

// Marker returns the opening marker for a workstation block.
func Marker(id string) string {
	return "# unkindled:" + id
}

// EndMarker returns the closing marker for a workstation block.
func EndMarker(id string) string {
	return "# end unkindled:" + id
}

// ValidateID rejects IDs that cannot be represented on a marker line.
func ValidateID(id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("workstation id is empty")
	}
	if strings.ContainsAny(id, "\r\n") {
		return fmt.Errorf("workstation id %q contains a newline", id)
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("workstation id %q contains a control character", id)
		}
	}
	return nil
}

// Build renders a managed block using LF line endings and a trailing newline.
func Build(b Block) (string, error) {
	if err := ValidateID(b.ID); err != nil {
		return "", err
	}
	if b.Alias == "" || strings.ContainsAny(b.Alias, " \t\r\n*?!,#\"'") {
		return "", fmt.Errorf("invalid SSH alias %q", b.Alias)
	}
	if b.HostName == "" || strings.ContainsAny(b.HostName, " \t\r\n") {
		return "", fmt.Errorf("invalid SSH host name %q", b.HostName)
	}
	if b.User == "" || strings.ContainsAny(b.User, " \t\r\n") {
		return "", fmt.Errorf("invalid SSH user %q", b.User)
	}
	if b.IdentityFile == "" || strings.ContainsAny(b.IdentityFile, "\r\n") {
		return "", fmt.Errorf("invalid identity file %q", b.IdentityFile)
	}

	var out strings.Builder
	out.WriteString(Marker(b.ID))
	out.WriteByte('\n')
	fmt.Fprintf(&out, "Host %s\n", b.Alias)
	fmt.Fprintf(&out, "  HostName %s\n", b.HostName)
	fmt.Fprintf(&out, "  User %s\n", b.User)
	if b.Port != 0 && b.Port != 22 {
		fmt.Fprintf(&out, "  Port %d\n", b.Port)
	}
	fmt.Fprintf(&out, "  IdentityFile %s\n", b.IdentityFile)
	out.WriteString("  IdentitiesOnly yes\n")
	out.WriteString(EndMarker(b.ID))
	out.WriteByte('\n')
	return out.String(), nil
}

// Upsert adds or replaces the managed block for b.ID. Other lines, comments,
// and ordering are preserved. The bool reports whether content changed.
func Upsert(content string, b Block) (string, bool, error) {
	block, err := Build(b)
	if err != nil {
		return content, false, err
	}

	eol := detectEOL(content)
	normalized := normalizeEOL(content)
	block = strings.TrimSuffix(block, "\n")

	start, end, found, err := findBlockLines(splitLines(normalized), b.ID)
	if err != nil {
		return content, false, err
	}

	var result string
	if found {
		lines := splitLines(normalized)
		blockLines := strings.Split(block, "\n")
		if equalLines(lines[start:end+1], blockLines) {
			return content, false, nil
		}
		updated := make([]string, 0, len(lines)-(end-start+1)+len(blockLines))
		updated = append(updated, lines[:start]...)
		updated = append(updated, blockLines...)
		updated = append(updated, lines[end+1:]...)
		result = strings.Join(updated, "\n")
		if result != "" {
			result += "\n"
		}
	} else {
		base := strings.TrimRight(normalized, "\n")
		if base != "" {
			result = base + "\n\n" + block + "\n"
		} else {
			result = block + "\n"
		}
	}

	if eol == "\r\n" {
		result = strings.ReplaceAll(result, "\n", "\r\n")
	}
	return result, result != content, nil
}

// Remove deletes the managed block for id, along with one separating blank
// line when it was added before an appended block. Other content is preserved.
func Remove(content, id string) (string, bool, error) {
	eol := detectEOL(content)
	normalized := normalizeEOL(content)
	lines := splitLines(normalized)
	start, end, found, err := findBlockLines(lines, id)
	if err != nil {
		return content, false, err
	}
	if !found {
		return content, false, nil
	}
	if start > 0 && strings.TrimSpace(lines[start-1]) == "" {
		start--
	}
	updated := make([]string, 0, len(lines)-(end-start+1))
	updated = append(updated, lines[:start]...)
	updated = append(updated, lines[end+1:]...)
	result := strings.Join(updated, "\n")
	if result != "" {
		result += "\n"
	}
	if eol == "\r\n" {
		result = strings.ReplaceAll(result, "\n", "\r\n")
	}
	return result, true, nil
}

// ManagedBlock is a parsed unkindled block.
type ManagedBlock struct {
	ID           string
	Alias        string
	HostName     string
	User         string
	Port         int
	IdentityFile string
	Raw          string
}

// Find returns the parsed managed block for id.
func Find(content, id string) (ManagedBlock, bool, error) {
	if err := ValidateID(id); err != nil {
		return ManagedBlock{}, false, err
	}
	lines := splitLines(normalizeEOL(content))
	start, end, found, err := findBlockLines(lines, id)
	if err != nil {
		return ManagedBlock{}, false, err
	}
	if !found {
		return ManagedBlock{}, false, nil
	}
	block, err := parseBlock(id, lines[start:end+1])
	if err != nil {
		return ManagedBlock{}, false, err
	}
	return block, true, nil
}

// IDs returns all well-formed opening marker IDs, in file order.
func IDs(content string) []string {
	var ids []string
	for _, line := range splitLines(normalizeEOL(content)) {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "# unkindled:") {
			continue
		}
		id := strings.TrimSpace(strings.TrimPrefix(trimmed, "# unkindled:"))
		if id != "" && ValidateID(id) == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

func findBlockLines(lines []string, id string) (int, int, bool, error) {
	if err := ValidateID(id); err != nil {
		return 0, 0, false, err
	}
	startMarker := Marker(id)
	endMarker := EndMarker(id)
	start := -1
	end := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != startMarker {
			continue
		}
		if start >= 0 {
			return 0, 0, false, fmt.Errorf("duplicate managed block for workstation %s", id)
		}
		start = i
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == endMarker {
				end = j
				break
			}
			if strings.TrimSpace(lines[j]) == startMarker {
				return 0, 0, false, fmt.Errorf("malformed managed block for workstation %s: nested start marker", id)
			}
		}
		if end < 0 {
			return 0, 0, false, fmt.Errorf("malformed managed block for workstation %s: missing %q", id, endMarker)
		}
	}
	if start < 0 {
		return 0, 0, false, nil
	}
	return start, end, true, nil
}

func parseBlock(id string, lines []string) (ManagedBlock, error) {
	block := ManagedBlock{ID: id, Raw: strings.Join(lines, "\n")}
	for _, line := range lines[1 : len(lines)-1] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := strings.ToLower(fields[0])
		value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))
		value = strings.Trim(value, `"`)
		switch key {
		case "host":
			if block.Alias == "" {
				block.Alias = value
			}
		case "hostname":
			block.HostName = value
		case "user":
			block.User = value
		case "identityfile":
			block.IdentityFile = value
		case "port":
			if port, err := strconv.Atoi(value); err == nil {
				block.Port = port
			}
		}
	}
	return block, nil
}

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func detectEOL(content string) string {
	if strings.Contains(content, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func normalizeEOL(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return strings.ReplaceAll(content, "\r", "\n")
}
