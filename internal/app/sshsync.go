package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/arthuRHD/unkindled-cli/internal/diff"
	"github.com/arthuRHD/unkindled-cli/internal/sshconf"
)

type sshKeyAction int

const (
	keyActionNone sshKeyAction = iota
	keyActionReveal
	keyActionChmod
)

type sshSyncItem struct {
	Plan       workstationPlan
	KeyAction  sshKeyAction
	ConfigDiff bool
	Existing   bool
}

func (a *App) runSSHSync(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("ssh sync", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	dryRun := fs.Bool("dry-run", false, "print the diff without changing any files")
	yes := fs.Bool("yes", false, "apply changes without confirmation")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: unkindled ssh sync [--dry-run] [--yes]

Fetch connection details for every workstation, write platform-generated
private keys to ~/.ssh/unkindled/, and add or replace managed blocks in
~/.ssh/config. Unrelated SSH config is preserved.

Options:
  --dry-run   Print the SSH config diff and key actions without changing files
  --yes       Apply changes without asking for confirmation
`)
	}
	if ok, code := parseFlags(fs, args); !ok {
		return code
	}

	_, client, err := a.loadClient()
	if err != nil {
		return a.fail("%v", err)
	}
	list, err := client.ListWorkstations(ctx)
	if err != nil {
		return a.fail("could not list workstations: %s", apiErrorText(err))
	}
	if len(list) == 0 {
		a.info("No workstations found.")
		return 0
	}

	existing, configPath, err := a.readSSHConfig()
	if err != nil {
		return a.fail("%v", err)
	}
	home, _ := a.homeDir()
	plans := a.buildPlans(ctx, client, list)

	var items []sshSyncItem
	hadErrors := false
	newContent := existing

	for _, plan := range plans {
		label := planDisplayName(plan.Workstation.Name, plan.Workstation.ID)
		if plan.Err != nil {
			a.warn("skipping %s: %v", label, plan.Err)
			hadErrors = true
			continue
		}
		if plan.ConnErr != nil {
			a.warn("skipping %s: could not load connection details: %s", label, apiErrorText(plan.ConnErr))
			hadErrors = true
			continue
		}
		conn := plan.Connection
		host := conn.Address()
		if host == "" {
			a.warn("skipping %s: the API returned no hostname or IPv4 address", label)
			hadErrors = true
			continue
		}

		exists, mode, size, err := inspectKey(plan.KeyPath)
		if err != nil {
			a.warn("skipping %s: %v", label, err)
			hadErrors = true
			continue
		}
		action := keyActionNone
		switch {
		case plan.PlatformKey && (!exists || size == 0):
			action = keyActionReveal
		case plan.PlatformKey && exists && mode.Perm() != 0o600:
			action = keyActionChmod
		case !plan.PlatformKey && (!exists || size == 0):
			a.warn("skipping %s: identity file %s does not exist", label, plan.KeyPath)
			hadErrors = true
			continue
		}

		// A hand-written Host block with the same alias is a working setup. Never append
		// a duplicate Host entry with the same alias; SSH would silently use the first one.
		if _, managed, _ := sshconf.Find(newContent, plan.Workstation.ID); !managed {
			if hostBlock, hostFound, hostErr := sshconf.FindHost(newContent, plan.Alias); hostErr != nil {
				a.warn("skipping %s: cannot read SSH host block: %v", label, hostErr)
				hadErrors = true
				continue
			} else if hostFound {
				addressOK := strings.EqualFold(hostBlock.HostName, host) &&
					hostBlock.User == conn.LoginUser() &&
					(hostBlock.Port == 0 || hostBlock.Port == conn.PortNumber())
				identityOK := cleanPathEqual(expandHome(hostBlock.IdentityFile, home), plan.KeyPath)
				if addressOK && identityOK {
					items = append(items, sshSyncItem{Plan: plan, KeyAction: action, Existing: true})
					continue
				}
				a.warn("skipping %s: SSH Host %q already exists with different settings; remove or rename that block first", label, plan.Alias)
				hadErrors = true
				continue
			}
		}

		block := sshconf.Block{
			ID:           plan.Workstation.ID,
			Alias:        plan.Alias,
			HostName:     host,
			User:         conn.LoginUser(),
			Port:         conn.PortNumber(),
			IdentityFile: plan.KeyPath,
		}
		updated, changed, err := sshconf.Upsert(newContent, block)
		if err != nil {
			a.warn("skipping %s: cannot update SSH config: %v", label, err)
			hadErrors = true
			continue
		}
		newContent = updated
		items = append(items, sshSyncItem{
			Plan:       plan,
			KeyAction:  action,
			ConfigDiff: changed,
		})
	}

	configChanged := newContent != existing
	keyActions := 0
	for _, item := range items {
		if item.KeyAction != keyActionNone {
			keyActions++
		}
	}
	if len(items) == 0 {
		a.warn("no workstations could be configured")
		return 1
	}
	if !configChanged && keyActions == 0 {
		a.info("SSH config and keys are already up to date for %d workstation(s).", len(items))
		if hadErrors {
			return 1
		}
		return 0
	}

	diffText := ""
	if configChanged {
		diffText = diff.Unified(existing, newContent, configPath, configPath)
	}
	a.renderSSHChanges(items, diffText)

	if *dryRun {
		a.info("Dry run: no files changed.")
		if hadErrors {
			return 1
		}
		return 0
	}

	if !*yes {
		ok, err := a.prompt("Apply these SSH changes?")
		if err != nil {
			return a.fail("could not read confirmation: %v", err)
		}
		if !ok {
			a.info("Aborted; no changes made.")
			return 1
		}
	}

	revealed := 0
	for _, item := range items {
		switch item.KeyAction {
		case keyActionReveal:
			key, err := client.RevealSSHKey(ctx, item.Plan.Workstation.ID)
			if err != nil {
				return a.fail("could not reveal SSH key for %s: %s", planDisplayName(item.Plan.Workstation.Name, item.Plan.Workstation.ID), apiErrorText(err))
			}
			if strings.TrimSpace(key) == "" {
				return a.fail("the API returned an empty SSH key for %s", planDisplayName(item.Plan.Workstation.Name, item.Plan.Workstation.ID))
			}
			if err := os.MkdirAll(filepath.Dir(item.Plan.KeyPath), 0o700); err != nil {
				return a.fail("could not create SSH key directory: %v", err)
			}
			if err := os.Chmod(filepath.Dir(item.Plan.KeyPath), 0o700); err != nil {
				return a.fail("could not secure SSH key directory: %v", err)
			}
			if err := writeFileAtomic(item.Plan.KeyPath, []byte(key), 0o600); err != nil {
				return a.fail("could not write SSH key for %s: %v", planDisplayName(item.Plan.Workstation.Name, item.Plan.Workstation.ID), err)
			}
			revealed++
		case keyActionChmod:
			if err := os.Chmod(item.Plan.KeyPath, 0o600); err != nil {
				return a.fail("could not set permissions on %s: %v", item.Plan.KeyPath, err)
			}
		}
	}

	if configChanged {
		if _, err := a.ensureSSHDir(); err != nil {
			return a.fail("%v", err)
		}
		if err := writeFileAtomic(configPath, []byte(newContent), 0o600); err != nil {
			return a.fail("could not write SSH config: %v", err)
		}
		a.info("Updated SSH config: %s", configPath)
	}
	if revealed > 0 {
		_, _, keyDir, _ := a.sshPaths()
		a.info("Wrote %d private key(s) to %s.", revealed, keyDir)
	}
	a.info("SSH setup complete.")
	if hadErrors {
		return 1
	}
	return 0
}

func inspectKey(path string) (exists bool, mode os.FileMode, size int64, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, 0, 0, nil
		}
		return false, 0, 0, fmt.Errorf("inspect identity file %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, 0, 0, fmt.Errorf("identity file %s is a symlink; refusing to manage it", path)
	}
	if !info.Mode().IsRegular() {
		return false, 0, 0, fmt.Errorf("identity file %s is not a regular file", path)
	}
	return true, info.Mode(), info.Size(), nil
}

func (a *App) renderSSHChanges(items []sshSyncItem, diffText string) {
	a.info("SSH setup changes:")
	for _, item := range items {
		label := planDisplayName(item.Plan.Workstation.Name, item.Plan.Workstation.ID)
		switch item.KeyAction {
		case keyActionReveal:
			a.info("  - %s: write platform-generated private key to %s", label, item.Plan.KeyPath)
		case keyActionChmod:
			a.info("  - %s: tighten private key permissions to 0600 (%s)", label, item.Plan.KeyPath)
		}
		if item.ConfigDiff {
			a.info("  - %s: update SSH config block (Host %s)", label, item.Plan.Alias)
		} else if item.Existing {
			a.info("  - %s: equivalent SSH config block already exists (Host %s); leaving it unchanged", label, item.Plan.Alias)
		}
	}
	if diffText != "" {
		fmt.Fprint(a.Stdout, diffText)
	}
}
