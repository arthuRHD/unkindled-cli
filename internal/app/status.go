package app

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/arthuRHD/unkindled-cli/internal/sshconf"
)

type statusSSH struct {
	BlockPresent bool   `json:"block_present"`
	Unmanaged    bool   `json:"unmanaged,omitempty"`
	Alias        string `json:"alias,omitempty"`
	AliasOK      bool   `json:"alias_ok"`
	IdentityFile string `json:"identity_file,omitempty"`
	IdentityOK   bool   `json:"identity_ok"`
	OK           bool   `json:"ok"`
}

type statusKey struct {
	Path              string `json:"path,omitempty"`
	Exists            bool   `json:"exists"`
	Mode              string `json:"mode,omitempty"`
	PlatformGenerated bool   `json:"platform_generated"`
	OK                bool   `json:"ok"`
}

type statusHerdrCheck struct {
	Available bool `json:"available"`
	Present   bool `json:"present"`
}

type statusWorkstation struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	State      string           `json:"state"`
	Plan       string           `json:"plan"`
	IPv4       string           `json:"ipv4"`
	SSH        statusSSH        `json:"ssh"`
	Key        statusKey        `json:"key"`
	Herdr      statusHerdrCheck `json:"herdr"`
	Configured bool             `json:"configured"`
	Issues     []string         `json:"issues"`
}

type statusMissing struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Issues []string `json:"issues"`
}

type statusReport struct {
	API          string              `json:"api"`
	Herdr        statusHerdrGlobal   `json:"herdr"`
	Workstations []statusWorkstation `json:"workstations"`
	Missing      []statusMissing     `json:"missing"`
}

type statusHerdrGlobal struct {
	Available bool   `json:"available"`
	Error     string `json:"error,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

func (a *App) runStatus(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	jsonOut := fs.Bool("json", false, "print machine-readable JSON")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: unkindled status [--json]

List workstations and check local configuration: managed SSH config blocks,
private key permissions, and herdr machine registration.

Options:
  --json   Print machine-readable JSON
`)
	}
	if ok, code := parseFlags(fs, args); !ok {
		return code
	}

	cfg, client, err := a.loadClient()
	if err != nil {
		return a.fail("%v", err)
	}
	list, err := client.ListWorkstations(ctx)
	if err != nil {
		return a.fail("could not list workstations: %s", apiErrorText(err))
	}
	plans := a.buildPlans(ctx, client, list)
	sshConfig, _, err := a.readSSHConfig()
	if err != nil {
		return a.fail("%v", err)
	}

	herdrState := statusHerdrGlobal{Available: a.herdrAvailable()}
	var machines []herdrMachine
	herdrKnown := false
	if herdrState.Available {
		machines, err = a.herdrList(ctx)
		if err != nil {
			herdrState.Error = err.Error()
		} else {
			herdrKnown = true
		}
	} else {
		herdrState.Hint = herdrInstallHint
	}

	report := statusReport{
		API:          cfg.APIURL,
		Herdr:        herdrState,
		Workstations: make([]statusWorkstation, 0, len(plans)),
		Missing:      []statusMissing{},
	}
	connectionIssues := 0
	home, _ := a.homeDir()

	for _, plan := range plans {
		ws := plan.Workstation
		row := statusWorkstation{
			ID:    ws.ID,
			Name:  ws.Name,
			State: orDash(ws.State),
			Plan:  orDash(ws.Plan),
			IPv4:  orDash(ws.Address()),
			Key: statusKey{
				Path:              plan.KeyPath,
				PlatformGenerated: plan.PlatformKey,
			},
			Herdr: statusHerdrCheck{Available: herdrState.Available},
		}
		issues := []string{}

		if plan.Err != nil {
			issues = append(issues, plan.Err.Error())
		}
		if plan.ConnErr != nil {
			issues = append(issues, "connection details unavailable: "+apiErrorText(plan.ConnErr))
			connectionIssues++
		}
		if row.IPv4 == "-" && plan.Connection.Address() != "" {
			row.IPv4 = orDash(plan.Connection.Address())
		}

		if plan.Err == nil {
			block, found, findErr := sshconf.Find(sshConfig, ws.ID)
			switch {
			case findErr != nil:
				issues = append(issues, "cannot read managed SSH block: "+findErr.Error())
			case found:
				row.SSH.BlockPresent = true
				row.SSH.Alias = block.Alias
				row.SSH.IdentityFile = block.IdentityFile
				row.SSH.AliasOK = block.Alias == plan.Alias
				row.SSH.IdentityOK = cleanPathEqual(expandHome(block.IdentityFile, home), plan.KeyPath)
				row.SSH.OK = row.SSH.AliasOK && row.SSH.IdentityOK
				if !row.SSH.AliasOK {
					issues = append(issues, fmt.Sprintf("SSH alias is %q (expected %q)", block.Alias, plan.Alias))
				}
				if !row.SSH.IdentityOK {
					issues = append(issues, fmt.Sprintf("SSH identity file is %q (expected %q)", block.IdentityFile, plan.KeyPath))
				}
			default:
				// An equivalent hand-written block (same Host alias, host, user, port,
				// identity file) is already a working setup. Detect it instead of
				// reporting it missing and adding a duplicate Host entry.
				host, hostFound, hostErr := sshconf.FindHost(sshConfig, plan.Alias)
				if hostErr != nil {
					issues = append(issues, "cannot read SSH host block: "+hostErr.Error())
					break
				}
				if !hostFound {
					issues = append(issues, "SSH config block missing")
					break
				}
				row.SSH.BlockPresent = true
				row.SSH.Unmanaged = true
				row.SSH.Alias = plan.Alias
				row.SSH.IdentityFile = host.IdentityFile
				addressOK := strings.EqualFold(host.HostName, plan.Connection.Address()) &&
					host.User == plan.Connection.LoginUser() &&
					(host.Port == 0 || host.Port == plan.Connection.PortNumber())
				row.SSH.AliasOK = addressOK
				row.SSH.IdentityOK = cleanPathEqual(expandHome(host.IdentityFile, home), plan.KeyPath)
				row.SSH.OK = addressOK && row.SSH.IdentityOK
				if !addressOK {
					issues = append(issues, fmt.Sprintf("SSH Host %q has a different HostName, User or Port (expected %s@%s:%d)", plan.Alias, plan.Connection.LoginUser(), plan.Connection.Address(), plan.Connection.PortNumber()))
				}
				if !row.SSH.IdentityOK {
					issues = append(issues, fmt.Sprintf("SSH identity file is %q (expected %q)", host.IdentityFile, plan.KeyPath))
				}
			}
			if !row.SSH.BlockPresent {
				row.SSH.OK = false
			}

			exists, mode, size, keyErr := inspectKey(plan.KeyPath)
			row.Key.Exists = exists && size > 0
			if exists {
				row.Key.Mode = fmt.Sprintf("%04o", mode.Perm())
			}
			switch {
			case keyErr != nil:
				issues = append(issues, keyErr.Error())
			case !exists || size == 0:
				issues = append(issues, "private key missing")
			case plan.PlatformKey && mode.Perm() != 0o600:
				issues = append(issues, fmt.Sprintf("private key mode is %04o (expected 0600)", mode.Perm()))
			}
			row.Key.OK = row.Key.Exists && (row.Key.PlatformGenerated && row.Key.Mode == "0600" || !row.Key.PlatformGenerated)
		}

		if herdrState.Available {
			if herdrKnown {
				row.Herdr.Present = herdrContains(machines, ws.Name, plan.Alias)
				if !row.Herdr.Present {
					issues = append(issues, "herdr machine missing")
				}
			}
		}

		row.Configured = plan.Err == nil && row.SSH.OK && row.Key.OK &&
			(!herdrKnown || row.Herdr.Present)
		row.Issues = issues
		report.Workstations = append(report.Workstations, row)
		if !row.Configured {
			report.Missing = append(report.Missing, statusMissing{ID: ws.ID, Name: ws.Name, Issues: issues})
		}
	}

	if *jsonOut {
		if err := a.writeJSON(report); err != nil {
			return a.fail("%v", err)
		}
		return 0
	}
	a.printStatus(report, connectionIssues)
	return 0
}

func (a *App) printStatus(report statusReport, connectionIssues int) {
	a.info("API: %s", report.API)
	if len(report.Workstations) == 0 {
		a.info("No workstations found.")
		return
	}
	a.info("")

	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tSTATE\tPLAN\tIPV4\tSSH\tKEY\tHERDR")
	for _, ws := range report.Workstations {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			displayCell(orDash(ws.Name)),
			displayCell(ws.State),
			displayCell(ws.Plan),
			displayCell(ws.IPv4),
			sshLabel(ws.SSH),
			keyLabel(ws.Key),
			herdrLabel(ws.Herdr),
		)
	}
	tw.Flush()

	if connectionIssues > 0 {
		a.info("")
		a.info("Note: connection details were unavailable for %d workstation(s); local checks used list data.", connectionIssues)
	}

	if !report.Herdr.Available {
		a.info("")
		a.info("herdr: not installed; herdr checks skipped.")
		a.info("Install: %s.", report.Herdr.Hint)
	} else if report.Herdr.Error != "" {
		a.info("")
		a.info("herdr: could not read machine list (%s).", report.Herdr.Error)
	}

	a.info("")
	if len(report.Missing) == 0 {
		a.info("All workstations are configured locally.")
		return
	}
	a.info("%d workstation(s) need configuration:", len(report.Missing))
	needSSH, needHerdr, needRetry := false, false, false
	for _, missing := range report.Missing {
		reasons := missing.Issues
		if len(reasons) == 0 {
			reasons = []string{"not fully configured"}
		}
		a.info("  - %s (%s): %s", displayCell(orDash(missing.Name)), displayCell(missing.ID), strings.Join(reasons, "; "))
		for _, reason := range reasons {
			lower := strings.ToLower(reason)
			switch {
			case strings.Contains(lower, "herdr"):
				needHerdr = true
			case strings.Contains(lower, "connection details"):
				needRetry = true
			default:
				needSSH = true
			}
		}
	}
	if needSSH || needHerdr || needRetry || report.Herdr.Error != "" {
		a.info("")
		a.info("Next steps:")
		if needRetry {
			a.info("  check your network, then run `unkindled status` again")
		}
		if needSSH {
			a.info("  unkindled ssh sync")
		}
		if needHerdr && report.Herdr.Available {
			a.info("  unkindled herdr sync")
		}
		if !report.Herdr.Available {
			a.info("  install herdr: %s", report.Herdr.Hint)
		}
		if report.Herdr.Available && report.Herdr.Error != "" && !needHerdr {
			a.info("  herdr machine list --json")
		}
	}
}

func sshLabel(check statusSSH) string {
	switch {
	case check.OK:
		return "ok"
	case check.BlockPresent:
		return "mismatch"
	default:
		return "missing"
	}
}

func keyLabel(check statusKey) string {
	if check.OK {
		return "ok"
	}
	if check.Exists {
		return "mode " + check.Mode
	}
	return "missing"
}

func herdrLabel(check statusHerdrCheck) string {
	if !check.Available {
		return "n/a"
	}
	if check.Present {
		return "ok"
	}
	return "missing"
}

func orDash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "-"
	}
	return value
}

func displayCell(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\r' || r == '\n' || r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, value)
}

func cleanPathEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.TrimRight(a, string(os.PathSeparator)) == strings.TrimRight(b, string(os.PathSeparator))
}
