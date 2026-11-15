package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/arthuRHD/unkindled-cli/internal/api"
	"github.com/arthuRHD/unkindled-cli/internal/config"
	"github.com/arthuRHD/unkindled-cli/internal/sshconf"
)

type doctorState struct {
	fails        int
	warns        int
	loginNext    bool
	chmodNext    string
	sshNext      bool
	apiNext      bool
	herdrNext    bool
	herdrMissing bool
}

func (a *App) runDoctor(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: unkindled doctor

Check local config, API connectivity, SSH configuration, private key
permissions, and herdr availability.
`)
	}
	if ok, code := parseFlags(fs, args); !ok {
		return code
	}

	d := &doctorState{}
	tw := tabwriter.NewWriter(a.Stdout, 0, 0, 2, ' ', 0)
	pass := func(label, detail string) {
		fmt.Fprintf(tw, "[PASS]\t%s\t%s\n", label, detail)
	}
	warn := func(label, detail string) {
		d.warns++
		fmt.Fprintf(tw, "[WARN]\t%s\t%s\n", label, detail)
	}
	fail := func(label, detail string) {
		d.fails++
		fmt.Fprintf(tw, "[FAIL]\t%s\t%s\n", label, detail)
	}

	configPath, _ := config.Path()
	var client *api.Client
	cfg, err := config.Load()
	switch {
	case errors.Is(err, config.ErrNotFound):
		fail("config", "not found at "+configPath)
		d.loginNext = true
	case err != nil:
		fail("config", err.Error())
		d.loginNext = true
	default:
		info, statErr := os.Stat(configPath)
		switch {
		case statErr != nil:
			warn("config", fmt.Sprintf("could not inspect %s: %v", configPath, statErr))
		case info.Mode().Perm()&0o077 != 0:
			fail("config", fmt.Sprintf("%s has mode %04o; expected 0600", configPath, info.Mode().Perm()))
			d.chmodNext = configPath
		default:
			pass("config", fmt.Sprintf("%s (mode %04o)", configPath, info.Mode().Perm()))
		}

		if strings.TrimSpace(cfg.Token) == "" {
			fail("token", "no API token stored")
			d.loginNext = true
		} else {
			pass("token", "stored and readable")
			client, err = api.New(cfg.APIURL, cfg.Token)
			if err != nil {
				fail("api", err.Error())
				d.loginNext = true
			} else {
				client.HTTP = a.httpClient()
			}
		}
	}

	if client != nil {
		workstations, err := client.ListWorkstations(ctx)
		if err != nil {
			fail("api", fmt.Sprintf("%s: %s", cfg.APIURL, apiErrorText(err)))
			var httpErr *api.HTTPError
			if errors.As(err, &httpErr) && httpErr.StatusCode == http.StatusUnauthorized {
				d.loginNext = true
			} else {
				d.apiNext = true
			}
		} else {
			pass("api", fmt.Sprintf("%s reachable (%d workstation(s))", cfg.APIURL, len(workstations)))
		}
	}

	a.doctorSSH(tw, pass, warn, fail, d)
	a.doctorHerdr(ctx, tw, pass, warn, fail, d)
	tw.Flush()

	a.info("")
	switch {
	case d.loginNext:
		a.info("Next: unkindled login")
	case d.chmodNext != "":
		a.info("Next: chmod 600 %s", d.chmodNext)
	case d.apiNext:
		a.info("Next: check your network, then run `unkindled status`")
	case d.sshNext:
		a.info("Next: unkindled ssh sync")
	case d.herdrMissing:
		a.info("Next: install herdr from %s", herdrInstallHint)
	case d.herdrNext:
		a.info("Next: check the herdr installation, then run `herdr machine list --json`")
	case d.warns > 0:
		a.info("Next: review the warnings above")
	default:
		a.info("Everything looks good.")
	}
	if d.fails > 0 {
		return 1
	}
	return 0
}

func (a *App) doctorSSH(tw *tabwriter.Writer, pass, warn, fail func(string, string), d *doctorState) {
	sshDir, configPath, keyDir, err := a.sshPaths()
	if err != nil {
		fail("ssh", err.Error())
		d.sshNext = true
		return
	}
	home, _ := a.homeDir()

	if info, err := os.Stat(sshDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			warn("ssh dir", sshDir+" does not exist; run `unkindled ssh sync` to create it")
			d.sshNext = true
		} else {
			fail("ssh dir", err.Error())
			d.sshNext = true
		}
	} else if info.Mode().Perm() != 0o700 {
		warn("ssh dir", fmt.Sprintf("%s has mode %04o; 0700 is recommended", sshDir, info.Mode().Perm()))
	} else {
		pass("ssh dir", sshDir+" (mode 0700)")
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			warn("ssh config", configPath+" does not exist; run `unkindled ssh sync`")
			d.sshNext = true
		} else {
			fail("ssh config", err.Error())
			d.sshNext = true
		}
	} else {
		info, statErr := os.Stat(configPath)
		if statErr == nil && info.Mode().Perm()&0o077 != 0 {
			warn("ssh config", fmt.Sprintf("%s has mode %04o; 0600 is recommended", configPath, info.Mode().Perm()))
		} else {
			pass("ssh config", configPath)
		}
		ids := sshconf.IDs(string(content))
		if len(ids) == 0 {
			warn("ssh entries", "no unkindled-managed blocks found; run `unkindled ssh sync`")
			d.sshNext = true
		}
		for _, id := range ids {
			block, found, findErr := sshconf.Find(string(content), id)
			if findErr != nil {
				fail("ssh entry "+id, findErr.Error())
				d.sshNext = true
				continue
			}
			if !found {
				continue
			}
			if block.IdentityFile == "" {
				fail("ssh key "+id, "managed block has no IdentityFile")
				d.sshNext = true
				continue
			}
			keyPath := expandHome(block.IdentityFile, home)
			keyInfo, statErr := os.Lstat(keyPath)
			switch {
			case errors.Is(statErr, os.ErrNotExist):
				fail("ssh key "+id, keyPath+" is missing")
				d.sshNext = true
			case statErr != nil:
				fail("ssh key "+id, statErr.Error())
				d.sshNext = true
			case keyInfo.Mode()&os.ModeSymlink != 0:
				fail("ssh key "+id, keyPath+" is a symlink")
				d.sshNext = true
			case !keyInfo.Mode().IsRegular():
				fail("ssh key "+id, keyPath+" is not a regular file")
				d.sshNext = true
			case keyInfo.Mode().Perm() != 0o600:
				fail("ssh key "+id, fmt.Sprintf("%s has mode %04o; expected 0600", keyPath, keyInfo.Mode().Perm()))
				d.sshNext = true
			case keyInfo.Size() == 0:
				fail("ssh key "+id, keyPath+" is empty")
				d.sshNext = true
			default:
				pass("ssh key "+id, keyPath+" (mode 0600)")
			}
		}
	}

	if info, err := os.Stat(keyDir); err == nil && !info.IsDir() {
		fail("ssh keys", keyDir+" is not a directory")
		d.sshNext = true
	}
}

func (a *App) doctorHerdr(ctx context.Context, tw *tabwriter.Writer, pass, warn, fail func(string, string), d *doctorState) {
	path, err := a.lookPath("herdr")
	if err != nil {
		warn("herdr", "not installed; install it from "+herdrInstallHint)
		d.herdrMissing = true
		return
	}
	machines, err := a.herdrList(ctx)
	if err != nil {
		fail("herdr", err.Error())
		d.herdrNext = true
		return
	}
	pass("herdr", fmt.Sprintf("%s (%d machine(s))", path, len(machines)))
}
