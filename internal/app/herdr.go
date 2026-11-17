package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const herdrInstallHint = "https://herdr.dev (make sure herdr is on PATH)"

type herdrMachine struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Name    string `json:"name"`
	Target  string `json:"target"`
	Session string `json:"session"`
}

func (a *App) herdrAvailable() bool {
	_, err := a.lookPath("herdr")
	return err == nil
}

func (a *App) herdrList(ctx context.Context) ([]herdrMachine, error) {
	stdout, stderr, err := a.runCommand(ctx, "herdr", "machine", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("herdr machine list failed: %s", commandDetail(stderr, err))
	}
	return decodeHerdrMachines(stdout)
}

func decodeHerdrMachines(data []byte) ([]herdrMachine, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var machines []herdrMachine
		if err := json.Unmarshal(trimmed, &machines); err != nil {
			return nil, fmt.Errorf("parse herdr machine list: %w", err)
		}
		return machines, nil
	}
	var wrapper struct {
		Machines []herdrMachine `json:"machines"`
		Data     []herdrMachine `json:"data"`
	}
	if err := json.Unmarshal(trimmed, &wrapper); err != nil {
		return nil, fmt.Errorf("parse herdr machine list: %w", err)
	}
	if wrapper.Machines != nil {
		return wrapper.Machines, nil
	}
	if wrapper.Data != nil {
		return wrapper.Data, nil
	}
	return nil, errors.New("parse herdr machine list: missing machine list")
}

func herdrContains(machines []herdrMachine, label, alias string) bool {
	for _, machine := range machines {
		for _, value := range []string{machine.Label, machine.Name, machine.Target} {
			if value != "" && (value == label || value == alias) {
				return true
			}
		}
	}
	return false
}

func (a *App) herdrAdd(ctx context.Context, alias, label string) error {
	args := []string{"machine", "add", alias, "--label", label, "--remote-session", "default"}
	_, stderr, err := a.runCommand(ctx, "herdr", args...)
	if err != nil {
		return fmt.Errorf("herdr machine add %s failed: %s", alias, commandDetail(stderr, err))
	}
	return nil
}

func (a *App) herdrRemove(ctx context.Context, profileID string) error {
	_, stderr, err := a.runCommand(ctx, "herdr", "machine", "remove", profileID)
	if err != nil {
		return fmt.Errorf("herdr machine remove %s failed: %s", profileID, commandDetail(stderr, err))
	}
	return nil
}

func herdrRemoveArgs(profileID string) []string {
	return []string{"machine", "remove", profileID}
}

func herdrRemoveCommandString(profileID, label string) string {
	parts := []string{"herdr"}
	for _, arg := range herdrRemoveArgs(profileID) {
		parts = append(parts, shellQuote(arg))
	}
	if label != "" {
		parts = append(parts, "# "+displayCell(label))
	}
	return strings.Join(parts, " ")
}

func herdrAddArgs(alias, label string) []string {
	return []string{"machine", "add", alias, "--label", label, "--remote-session", "default"}
}

func herdrCommandString(alias, label string) string {
	parts := []string{"herdr"}
	for _, arg := range herdrAddArgs(alias, label) {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func commandDetail(stderr []byte, err error) string {
	detail := strings.TrimSpace(string(stderr))
	if len(detail) > 500 {
		detail = detail[:500] + "..."
	}
	if detail == "" {
		return err.Error()
	}
	return fmt.Sprintf("%v: %s", err, detail)
}
