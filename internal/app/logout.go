package app

import (
	"context"
	"flag"
	"fmt"

	"github.com/arthuRHD/unkindled-cli/internal/config"
)

func (a *App) runLogout(_ context.Context, args []string) int {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: unkindled logout

Remove the local token and config file. The token is not revoked server-side
by this CLI; it remains valid until it expires.
`)
	}
	if ok, code := parseFlags(fs, args); !ok {
		return code
	}

	path, removed, err := config.Delete()
	if err != nil {
		return a.fail("%v", err)
	}
	if !removed {
		a.info("Not logged in; no config found at %s.", path)
		return 0
	}
	a.info("Logged out. Removed %s.", path)
	a.info("Note: the API token was not revoked server-side (this CLI has no revocation flow); it remains valid until it expires.")
	return 0
}
