package app

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/arthuRHD/unkindled-cli/internal/api"
)

func (a *App) runStop(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	jsonOut := fs.Bool("json", false, "print machine-readable JSON")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: unkindled stop <workstation> [--json]

Request a graceful stop. The workstation can be started again later.
`)
	}
	if err := fs.Parse(flagsFirst(args)); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	_, client, err := a.loadClient()
	if err != nil {
		return a.fail("%v", err)
	}
	ws, err := resolveWorkstation(ctx, client, fs.Arg(0))
	if err != nil {
		return a.fail("%v", err)
	}
	if err := client.StopWorkstation(ctx, ws.ID); err != nil {
		return a.fail("could not stop %s: %s", displayCell(ws.Name), apiErrorText(err))
	}
	if *jsonOut {
		_ = a.writeJSON(map[string]any{"action": "stop", "workstation": ws})
		return 0
	}
	a.info("Stop requested for %s (%s).", displayCell(ws.Name), ws.ID)
	a.info("Run `unkindled status` to watch the state, or `unkindled kill %s` to delete it.", displayCell(ws.Name))
	return 0
}

func (a *App) runKill(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("kill", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	yes := fs.Bool("yes", false, "delete without asking for confirmation")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: unkindled kill <workstation> [--yes] [--json]

Permanently delete a workstation, its disk, public IP, SSH key, and checkpoints.
This cannot be undone.
`)
	}
	if err := fs.Parse(flagsFirst(args)); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	_, client, err := a.loadClient()
	if err != nil {
		return a.fail("%v", err)
	}
	ws, err := resolveWorkstation(ctx, client, fs.Arg(0))
	if err != nil {
		return a.fail("%v", err)
	}
	if !*yes {
		ok, err := a.prompt(fmt.Sprintf("Kill %s permanently, including its disk and checkpoints?", displayCell(ws.Name)))
		if err != nil {
			return a.fail("could not read confirmation: %v", err)
		}
		if !ok {
			a.info("Aborted; nothing was deleted.")
			return 1
		}
	}
	if err := client.KillWorkstation(ctx, ws.ID); err != nil {
		return a.fail("could not kill %s: %s", displayCell(ws.Name), apiErrorText(err))
	}
	if *jsonOut {
		_ = a.writeJSON(map[string]any{"action": "kill", "workstation": ws})
		return 0
	}
	a.info("Deletion requested for %s (%s).", displayCell(ws.Name), ws.ID)
	a.info("Its disk, IP, SSH key, and checkpoints will be removed. Run `unkindled status` to confirm.")
	return 0
}

func (a *App) runCreate(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	templateRef := fs.String("template", "", "template id, name, or slug")
	name := fs.String("name", "", "workstation name (defaults to the template name)")
	plan := fs.String("plan", "", "plan id override (solo, pro, studio)")
	sshKeyPath := fs.String("ssh-key", "", "path to an SSH public key")
	repo := fs.String("repo", "", "repository URL override")
	branch := fs.String("branch", "", "repository branch override")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: unkindled create --template <id|name|slug> [options]

Create a workstation from a template.

Options:
  --name <name>       Workstation name; defaults to the template name
  --plan <plan>       solo, pro, or studio; defaults to the template plan
  --ssh-key <path>    Public key to install (private key stays local)
  --repo <url>        Repository URL override
  --branch <branch>   Repository branch override
  --json              Print machine-readable JSON
`)
	}
	if ok, code := parseFlags(fs, args); !ok {
		return code
	}
	if strings.TrimSpace(*templateRef) == "" {
		fs.Usage()
		return 2
	}
	_, client, err := a.loadClient()
	if err != nil {
		return a.fail("%v", err)
	}
	template, err := resolveTemplate(ctx, client, *templateRef)
	if err != nil {
		return a.fail("%v", err)
	}
	publicKey := ""
	if *sshKeyPath != "" {
		publicKey, err = a.readPublicKey(*sshKeyPath)
		if err != nil {
			return a.fail("%v", err)
		}
	}
	in := api.LaunchTemplateRequest{
		Name: *name, Plan: *plan, SSHPublicKey: publicKey,
		RepositoryURL: *repo, RepositoryBranch: *branch,
	}
	ws, err := client.LaunchTemplate(ctx, template.ID, in)
	if err != nil {
		return a.fail("could not create a workstation from %s: %s", displayCell(template.Name), apiErrorText(err))
	}
	if *jsonOut {
		_ = a.writeJSON(map[string]any{"workstation": ws, "template": template})
		return 0
	}
	a.info("Workstation %s requested from template %s.", displayCell(ws.Name), displayCell(template.Name))
	a.info("State: %s", displayCell(orDash(ws.State)))
	a.info("Next: run `unkindled ssh sync` once it is ready, or `unkindled status` to watch it.")
	return 0
}

func (a *App) runTemplates(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("templates", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	jsonOut := fs.Bool("json", false, "print machine-readable JSON")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: unkindled templates [--json]

List workstation templates available to the signed-in user.
`)
	}
	if ok, code := parseFlags(fs, args); !ok {
		return code
	}
	_, client, err := a.loadClient()
	if err != nil {
		return a.fail("%v", err)
	}
	list, err := client.ListTemplates(ctx)
	if err != nil {
		return a.fail("could not list templates: %s", apiErrorText(err))
	}
	if *jsonOut {
		_ = a.writeJSON(map[string]any{"templates": list})
		return 0
	}
	if len(list) == 0 {
		a.info("No templates found.")
		return 0
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSLUG\tPLAN\tSOURCE\tDESCRIPTION")
	for _, t := range list {
		source := "custom"
		if t.Builtin {
			source = "built-in"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			displayCell(t.Name), displayCell(orDash(t.Slug)), displayCell(orDash(t.Plan)), source, displayCell(t.Description))
	}
	tw.Flush()
	return 0
}

func resolveWorkstation(ctx context.Context, client *api.Client, ref string) (api.Workstation, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return api.Workstation{}, fmt.Errorf("workstation name or id is required")
	}
	list, err := client.ListWorkstations(ctx)
	if err != nil {
		return api.Workstation{}, fmt.Errorf("list workstations: %w", err)
	}
	var matches []api.Workstation
	for _, ws := range list {
		if ws.ID == ref || strings.EqualFold(ws.Name, ref) {
			matches = append(matches, ws)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return api.Workstation{}, fmt.Errorf("workstation %q is ambiguous; use its id", ref)
	}
	return api.Workstation{}, fmt.Errorf("workstation %q not found", ref)
}

func resolveTemplate(ctx context.Context, client *api.Client, ref string) (api.Template, error) {
	ref = strings.TrimSpace(ref)
	list, err := client.ListTemplates(ctx)
	if err != nil {
		return api.Template{}, fmt.Errorf("list templates: %w", err)
	}
	var matches []api.Template
	for _, t := range list {
		if t.ID == ref || strings.EqualFold(t.Name, ref) || strings.EqualFold(t.Slug, ref) {
			matches = append(matches, t)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return api.Template{}, fmt.Errorf("template %q is ambiguous; use its id", ref)
	}
	return api.Template{}, fmt.Errorf("template %q not found", ref)
}

// flagsFirst lets users put boolean flags after the workstation name, e.g.
// `unkindled kill dev-box --yes`, while still using the standard flag package.
func flagsFirst(args []string) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			continue
		}
		positional = append(positional, arg)
	}
	return append(flags, positional...)
}

func (a *App) readPublicKey(path string) (string, error) {
	home, err := a.homeDir()
	if err != nil {
		return "", err
	}
	path = expandHome(strings.TrimSpace(path), home)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("SSH public key path must be absolute or start with ~/")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read SSH public key %s: %w", path, err)
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", fmt.Errorf("SSH public key %s is empty", path)
	}
	if len(key) > 16*1024 {
		return "", fmt.Errorf("SSH public key %s is too large", path)
	}
	return key, nil
}
