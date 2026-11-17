package app

import (
	"context"
	"flag"
	"fmt"
	"strings"
)

func (a *App) runHerdrSync(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("herdr sync", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	printOnly := fs.Bool("print", false, "print herdr commands without running them")
	dryRun := fs.Bool("dry-run", false, "show what would change without changing anything")
	yes := fs.Bool("yes", false, "apply changes without confirmation")
	prune := fs.Bool("prune", false, "remove saved machines for workstations that no longer exist")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: unkindled herdr sync [--prune] [--print] [--dry-run] [--yes]

Register each workstation with herdr using its SSH alias.

Options:
  --prune     Remove saved machines whose workstation no longer exists
  --print     Print the herdr commands instead of running them
  --dry-run   Show the commands that would run without changing anything
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
	if len(list) == 0 && !*prune {
		a.info("No workstations found.")
		return 0
	}
	plans := a.buildPlans(ctx, client, list)

	available := a.herdrAvailable()
	var existing []herdrMachine
	if available {
		existing, err = a.herdrList(ctx)
		if err != nil {
			if *printOnly {
				a.warn("%v; printing commands for every workstation", err)
			} else if *dryRun {
				a.warn("%v; cannot determine which workstations are already registered", err)
			} else {
				return a.fail("%v", err)
			}
		}
	}

	if !available {
		fmt.Fprintf(a.Stderr, "Warning: herdr is not installed; install it from %s.\n", herdrInstallHint)
		if *printOnly {
			for _, plan := range plans {
				label := planDisplayName(plan.Workstation.Name, plan.Workstation.ID)
				if plan.Err != nil {
					a.warn("skipping %s: %v", label, plan.Err)
					continue
				}
				if plan.ConnErr != nil {
					a.warn("skipping %s: could not load connection details: %s", label, apiErrorText(plan.ConnErr))
					continue
				}
				a.info("%s", herdrCommandString(plan.Alias, plan.Workstation.Name))
			}
			return 0
		}
		if *dryRun {
			return 0
		}
		return 1
	}

	type pendingAdd struct {
		plan  workstationPlan
		alias string
		label string
	}
	var pending []pendingAdd
	hadErrors := false
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
		if herdrContains(existing, plan.Workstation.Name, plan.Alias) {
			continue
		}
		pending = append(pending, pendingAdd{plan: plan, alias: plan.Alias, label: plan.Workstation.Name})
	}

	activeAliases := make(map[string]bool, len(plans))
	for _, plan := range plans {
		if plan.Alias != "" {
			activeAliases[plan.Alias] = true
		}
	}
	var stale []herdrMachine
	if *prune {
		for _, machine := range existing {
			target := strings.TrimSpace(machine.Target)
			// The CLI creates aliases as unkindled-<slug>. Do not touch unrelated
			// user-created Herdr machines.
			if !strings.HasPrefix(target, "unkindled-") || activeAliases[target] {
				continue
			}
			stale = append(stale, machine)
		}
	}

	for _, item := range pending {
		if *printOnly || *dryRun {
			a.info("%s", herdrCommandString(item.alias, item.label))
		}
	}
	for _, machine := range stale {
		if *printOnly || *dryRun {
			a.info("%s", herdrRemoveCommandString(machine.ID, machine.Label))
		}
	}
	if len(pending) == 0 && len(stale) == 0 {
		a.info("herdr is already up to date for %d workstation(s).", len(plans))
		if hadErrors {
			return 1
		}
		return 0
	}
	if *printOnly || *dryRun {
		if hadErrors {
			return 1
		}
		return 0
	}

	if !*yes {
		a.info("")
		if len(pending) > 0 {
			a.info("This will register %d workstation(s) with herdr:", len(pending))
			for _, item := range pending {
				a.info("  %s", herdrCommandString(item.alias, item.label))
			}
		}
		if len(stale) > 0 {
			a.info("This will remove %d stale Herdr machine(s):", len(stale))
			for _, machine := range stale {
				a.info("  %s", herdrRemoveCommandString(machine.ID, machine.Label))
			}
		}
		a.info("")
		ok, err := a.prompt("Apply these changes?")
		if err != nil {
			return a.fail("could not read confirmation: %v", err)
		}
		if !ok {
			a.info("Aborted; no changes made.")
			return 1
		}
	}

	failed := false
	for _, machine := range stale {
		if err := a.herdrRemove(ctx, machine.ID); err != nil {
			a.warn("%v", err)
			failed = true
			continue
		}
		a.info("Removed stale Herdr machine %s.", planDisplayName(machine.Label, machine.ID))
	}
	for _, item := range pending {
		if err := a.herdrAdd(ctx, item.alias, item.label); err != nil {
			a.warn("%v", err)
			failed = true
			continue
		}
		a.info("Registered %s with herdr as %s.", item.label, item.alias)
	}
	if failed || hadErrors {
		return 1
	}
	return 0
}

func planDisplayName(name, id string) string {
	name = strings.TrimSpace(displayCell(strings.TrimSpace(name)))
	if name != "" {
		return name
	}
	return displayCell(id)
}
