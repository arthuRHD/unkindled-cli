// Package app implements the unkindled command-line interface.
package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/arthuRHD/unkindled-cli/internal/api"
	"github.com/arthuRHD/unkindled-cli/internal/config"
	"github.com/arthuRHD/unkindled-cli/internal/name"
	"github.com/arthuRHD/unkindled-cli/internal/sshconf"
)

// Version is the CLI version.
const Version = "1.2.0"

// App holds the CLI's dependencies so commands can be tested end to end.
type App struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	HTTPClient *http.Client
	Sleep      func(time.Duration)
	LookPath   func(string) (string, error)
	Exec       func(ctx context.Context, name string, args ...string) ([]byte, []byte, error)
}

// New returns an App wired to the process streams.
func New() *App {
	return &App{
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Stdin:    os.Stdin,
		Sleep:    time.Sleep,
		LookPath: exec.LookPath,
	}
}

// Run dispatches a command and returns the process exit code.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		a.printUsage()
		return 0
	}

	switch args[0] {
	case "help", "-h", "--help":
		a.printUsage()
		return 0
	case "version", "--version", "-v":
		fmt.Fprintf(a.Stdout, "unkindled %s\n", Version)
		return 0
	case "login":
		return a.runLogin(ctx, args[1:])
	case "status", "workstations":
		return a.runStatus(ctx, args[1:])
	case "ssh":
		return a.runSSH(ctx, args[1:])
	case "herdr":
		return a.runHerdr(ctx, args[1:])
	case "doctor":
		return a.runDoctor(ctx, args[1:])
	case "logout":
		return a.runLogout(ctx, args[1:])
	default:
		fmt.Fprintf(a.Stderr, "unknown command %q\n\n", args[0])
		a.printUsageTo(a.Stderr)
		return 2
	}
}

func (a *App) printUsage() {
	a.printUsageTo(a.Stdout)
}

func (a *App) printUsageTo(w io.Writer) {
	fmt.Fprint(w, `unkindled connects your workstations to your local machine.

Usage:
  unkindled <command> [options]

Commands:
  login        Sign in with GitHub device flow, or store an API token
  status       Show workstations and whether they are configured locally
  ssh sync     Write SSH keys and managed ~/.ssh/config entries
  herdr sync   Register workstations with herdr
  doctor       Diagnose config, connectivity, SSH, and herdr
  logout       Remove the local token and config
  version      Print the version

Aliases:
  workstations   Same as status

Run "unkindled <command> --help" for command-specific options.
`)
}

func (a *App) runSSH(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.Stderr, "usage: unkindled ssh sync [--dry-run] [--yes]\n")
		return 2
	}
	switch args[0] {
	case "sync":
		return a.runSSHSync(ctx, args[1:])
	case "help", "-h", "--help":
		fmt.Fprint(a.Stdout, `Usage: unkindled ssh sync [--dry-run] [--yes]

Fetch connection details for every workstation, reveal platform-generated
private keys when needed, and add or update managed blocks in ~/.ssh/config.

Options:
  --dry-run   Show the SSH config diff and key actions without changing files
  --yes       Apply changes without asking for confirmation
`)
		return 0
	default:
		fmt.Fprintf(a.Stderr, "unknown ssh subcommand %q (expected \"sync\")\n", args[0])
		return 2
	}
}

func (a *App) runHerdr(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.Stderr, "usage: unkindled herdr sync [--print] [--dry-run] [--yes]\n")
		return 2
	}
	switch args[0] {
	case "sync":
		return a.runHerdrSync(ctx, args[1:])
	case "help", "-h", "--help":
		fmt.Fprint(a.Stdout, `Usage: unkindled herdr sync [--print] [--dry-run] [--yes]

Register each workstation with herdr as an SSH machine.

Options:
  --print     Print the herdr commands instead of running them
  --dry-run   Show the commands that would run without changing anything
  --yes       Apply changes without asking for confirmation
`)
		return 0
	default:
		fmt.Fprintf(a.Stderr, "unknown herdr subcommand %q (expected \"sync\")\n", args[0])
		return 2
	}
}

func (a *App) fail(format string, args ...any) int {
	fmt.Fprintf(a.Stderr, "Error: "+format+"\n", args...)
	return 1
}

func (a *App) warn(format string, args ...any) {
	fmt.Fprintf(a.Stderr, "Warning: "+format+"\n", args...)
}

func (a *App) info(format string, args ...any) {
	fmt.Fprintf(a.Stdout, format+"\n", args...)
}

func (a *App) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// loadClient loads config and constructs an authenticated API client.
func (a *App) loadClient() (*config.Config, *api.Client, error) {
	cfg, err := config.Load()
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return nil, nil, errors.New("not logged in; run `unkindled login`")
		}
		return nil, nil, err
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, nil, errors.New("no API token stored; run `unkindled login`")
	}
	client, err := api.New(cfg.APIURL, cfg.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("%v; run `unkindled login --api <url>` to set a valid API URL", err)
	}
	client.HTTP = a.httpClient()
	return cfg, client, nil
}

func (a *App) writeJSON(value any) error {
	encoder := json.NewEncoder(a.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("write JSON output: %w", err)
	}
	return nil
}

func (a *App) prompt(question string) (bool, error) {
	if a.Stdin == nil {
		return false, nil
	}
	fmt.Fprintf(a.Stdout, "%s [y/N] ", question)
	line, err := bufio.NewReader(a.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func parseFlags(fs *flag.FlagSet, args []string) (bool, int) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, 0
		}
		return false, 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(fs.Output(), "unexpected argument %q\n", fs.Arg(0))
		fs.Usage()
		return false, 2
	}
	return true, 0
}

func apiErrorText(err error) string {
	var httpErr *api.HTTPError
	if errors.As(err, &httpErr) {
		if hint := httpErr.Hint(); hint != "" {
			return err.Error() + "; " + hint
		}
	}
	return err.Error()
}

func (a *App) homeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	if home == "" {
		return "", errors.New("locate home directory: HOME is not set")
	}
	return home, nil
}

func (a *App) sshPaths() (dir, configPath, keyDir string, err error) {
	home, err := a.homeDir()
	if err != nil {
		return "", "", "", err
	}
	dir = filepath.Join(home, ".ssh")
	return dir, filepath.Join(dir, "config"), filepath.Join(dir, "unkindled"), nil
}

func expandHome(path, home string) string {
	if path == "~" {
		return home
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	return path
}

// workstationPlan is the shared local plan for one workstation.
type workstationPlan struct {
	Workstation api.Workstation
	Slug        string
	Alias       string
	KeyPath     string
	Connection  api.Connection
	ConnErr     error
	PlatformKey bool
	Err         error
}

func uniqueSlugs(list []api.Workstation) map[string]string {
	slugs := make(map[string]string, len(list))
	used := make(map[string]bool, len(list))
	for _, ws := range list {
		slug, err := name.Safe(ws.Name)
		if err != nil {
			continue
		}
		candidate := slug
		if used[candidate] {
			candidate = slug + "-" + shortID(ws.ID, len(list))
		}
		for used[candidate] {
			candidate += "-x"
		}
		used[candidate] = true
		slugs[ws.ID] = candidate
	}
	return slugs
}

func shortID(id string, fallback int) string {
	var b strings.Builder
	for _, r := range strings.ToLower(id) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
		if b.Len() == 8 {
			break
		}
	}
	if b.Len() == 0 {
		return strconv.Itoa(fallback)
	}
	return b.String()
}

// buildPlans derives names, aliases, key paths, and connection details for each
// workstation. API connection calls happen once per workstation.
func (a *App) buildPlans(ctx context.Context, client *api.Client, list []api.Workstation) []workstationPlan {
	_, _, keyDir, pathErr := a.sshPaths()
	slugs := uniqueSlugs(list)
	plans := make([]workstationPlan, 0, len(list))
	usedAliases := make(map[string]bool, len(list))

	for _, ws := range list {
		plan := workstationPlan{Workstation: ws}
		plan.Slug = slugs[ws.ID]
		if plan.Slug == "" {
			if _, err := name.Safe(ws.Name); err != nil {
				plan.Err = err
			} else {
				plan.Err = fmt.Errorf("could not derive a local name")
			}
		}
		if pathErr != nil && plan.Err == nil {
			plan.Err = pathErr
		}
		if plan.Err == nil {
			if err := sshconf.ValidateID(ws.ID); err != nil {
				plan.Err = err
			}
		}

		if plan.Err == nil {
			conn, err := client.GetConnection(ctx, ws.ID)
			if err != nil {
				plan.ConnErr = err
			} else {
				plan.Connection = conn
			}
			plan.PlatformKey = plan.Connection.Generated() || ws.PlatformGeneratedKey
		}

		if plan.Err == nil {
			if home, err := a.homeDir(); err == nil {
				// The API's identity_file is authoritative. This matters for an existing
				// platform-generated key that was imported under its original path: the
				// CLI must detect it instead of planning a second key under .ssh/unkindled.
				if preferred := plan.Connection.PreferredKeyPath(); preferred != "" {
					preferred = expandHome(preferred, home)
					if !filepath.IsAbs(preferred) {
						preferred = filepath.Join(home, preferred)
					}
					plan.KeyPath = preferred
				} else {
					plan.KeyPath = filepath.Join(keyDir, plan.Slug)
				}
			}
		}

		if plan.Err == nil {
			alias := plan.Connection.PreferredAlias()
			if alias == "" {
				alias = ws.SSHAlias
			}
			if alias != "" && name.ValidateAlias(alias) != nil {
				alias = ""
			}
			if alias == "" {
				alias = name.Alias(plan.Slug)
			}
			if err := name.ValidateAlias(alias); err != nil {
				plan.Err = err
			} else if usedAliases[alias] {
				alias = name.Alias(plan.Slug)
				for usedAliases[alias] {
					alias += "-x"
				}
			}
			plan.Alias = alias
			usedAliases[alias] = true
		}

		plans = append(plans, plan)
	}
	return plans
}

// readSSHConfig reads ~/.ssh/config, returning an empty string when absent.
func (a *App) readSSHConfig() (string, string, error) {
	_, configPath, _, err := a.sshPaths()
	if err != nil {
		return "", "", err
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", configPath, nil
		}
		return "", configPath, fmt.Errorf("read SSH config %s: %w", configPath, err)
	}
	return string(data), configPath, nil
}

// ensureSSHDir creates ~/.ssh with mode 0700 when missing.
func (a *App) ensureSSHDir() (string, error) {
	dir, _, _, err := a.sshPaths()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create SSH directory %s: %w", dir, err)
	}
	return dir, nil
}

// writeFileAtomic writes data with mode, refusing to follow a symlink.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write through symlink %s", path)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".unkindled-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("set permissions on %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("set permissions on %s: %w", path, err)
	}
	return nil
}

func (a *App) runCommand(ctx context.Context, command string, args ...string) ([]byte, []byte, error) {
	if a.Exec != nil {
		return a.Exec(ctx, command, args...)
	}
	cmd := exec.CommandContext(ctx, command, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if a.Stdin != nil {
		cmd.Stdin = a.Stdin
	}
	err := cmd.Run()
	return []byte(stdout.String()), []byte(stderr.String()), err
}

func (a *App) lookPath(file string) (string, error) {
	if a.LookPath != nil {
		return a.LookPath(file)
	}
	return exec.LookPath(file)
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	safe := true
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '@' || r == '%' || r == '+' || r == '=' || r == ',') {
			safe = false
			break
		}
	}
	if safe {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func (a *App) sleep(duration time.Duration) {
	if a.Sleep != nil {
		a.Sleep(duration)
		return
	}
	time.Sleep(duration)
}
