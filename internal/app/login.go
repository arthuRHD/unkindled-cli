package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/arthuRHD/unkindled-cli/internal/api"
	"github.com/arthuRHD/unkindled-cli/internal/config"
)

func (a *App) runLogin(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	defaultAPI := config.DefaultAPIURL
	if existing, err := config.Load(); err == nil && existing.APIURL != "" {
		defaultAPI = existing.APIURL
	}
	apiURL := fs.String("api", defaultAPI, "control plane URL")
	token := fs.String("token", "", "store a token without running the device flow")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: unkindled login [--api URL] [--token TOKEN] [--json]

Sign in to Unkindled with the GitHub device flow and store the API token.
The token is written to the config file with mode 0600 and is never printed.

Options:
  --api URL     Control plane URL (default https://api.unkindled.dev)
  --token TOKEN Store this API token without running the device flow
  --json        Print machine-readable JSON (never includes the token)
`)
	}
	if ok, code := parseFlags(fs, args); !ok {
		return code
	}

	normalized, err := api.NormalizeBaseURL(*apiURL)
	if err != nil {
		return a.fail("%v", err)
	}

	if strings.TrimSpace(*token) != "" {
		return a.storeToken(normalized, strings.TrimSpace(*token), *jsonOut)
	}
	return a.deviceLogin(ctx, normalized, *jsonOut)
}

func (a *App) storeToken(apiURL, token string, jsonOut bool) int {
	cfg := &config.Config{APIURL: apiURL, Token: token}
	if existing, err := config.Load(); err == nil && existing.APIURL == apiURL {
		cfg.User = existing.User
	}
	path, err := config.Save(cfg)
	if err != nil {
		return a.fail("could not store token: %v", err)
	}
	if jsonOut {
		if err := a.writeJSON(map[string]any{
			"status": "complete",
			"api":    apiURL,
			"config": path,
		}); err != nil {
			return a.fail("%v", err)
		}
		return 0
	}
	a.info("Token stored in %s (mode 0600).", path)
	a.info("Run `unkindled status` to see your workstations.")
	return 0
}

func (a *App) deviceLogin(ctx context.Context, apiURL string, jsonOut bool) int {
	client, err := api.New(apiURL, "")
	if err != nil {
		return a.fail("%v", err)
	}
	client.HTTP = a.httpClient()

	start, err := client.StartGitHubDevice(ctx)
	if err != nil {
		return a.fail("could not start GitHub device login: %s", apiErrorText(err))
	}

	expiresIn := time.Duration(start.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = 15 * time.Minute
	}
	interval := time.Duration(start.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if interval > 60*time.Second {
		interval = 60 * time.Second
	}
	deadline := time.Now().Add(expiresIn)

	if jsonOut {
		if err := a.writeJSON(map[string]any{
			"status":           "pending",
			"user_code":        start.UserCode,
			"verification_uri": start.VerificationURI,
			"expires_in":       int(expiresIn / time.Second),
			"interval":         int(interval / time.Second),
		}); err != nil {
			return a.fail("%v", err)
		}
	} else {
		a.info("To sign in, open %s", start.VerificationURI)
		a.info("and enter code: %s", start.UserCode)
		a.info("Waiting for authorization...")
	}

	networkErrors := 0
	for {
		if time.Now().After(deadline) {
			return a.fail("the device code expired before authorization; run `unkindled login` again")
		}

		poll, err := client.PollGitHubDevice(ctx, start.DeviceCode)
		if err != nil {
			if ctx.Err() != nil {
				return a.fail("login canceled")
			}
			var httpErr *api.HTTPError
			if errors.As(err, &httpErr) {
				return a.fail("could not check GitHub authorization: %s", apiErrorText(err))
			}
			networkErrors++
			if networkErrors >= 3 {
				return a.fail("could not check GitHub authorization: %s", apiErrorText(err))
			}
			a.warn("network problem while waiting for authorization (%v); retrying", err)
			a.sleep(interval)
			continue
		}
		networkErrors = 0

		switch strings.ToLower(strings.TrimSpace(poll.Status)) {
		case "pending", "authorization_pending":
			if poll.Interval > 0 {
				interval = time.Duration(poll.Interval) * time.Second
			}
			interval = clampInterval(interval)
			a.sleep(interval)
		case "slow_down":
			if poll.Interval > 0 {
				interval = time.Duration(poll.Interval) * time.Second
			} else {
				interval += 5 * time.Second
			}
			interval = clampInterval(interval)
			a.sleep(interval)
		case "complete", "authorized", "success":
			token := strings.TrimSpace(poll.Token)
			if token == "" {
				return a.fail("GitHub authorization completed but the API returned no token; run `unkindled login` again")
			}
			return a.finishLogin(apiURL, token, poll.User, jsonOut)
		case "expired":
			return a.fail("the device code expired before authorization; run `unkindled login` again")
		case "denied", "access_denied":
			return a.fail("GitHub authorization was denied; run `unkindled login` again to retry")
		default:
			return a.fail("unexpected device login status %q; run `unkindled login` again", poll.Status)
		}
	}
}

func (a *App) finishLogin(apiURL, token string, user map[string]any, jsonOut bool) int {
	path, err := config.Save(&config.Config{
		APIURL: apiURL,
		Token:  token,
		User:   user,
	})
	if err != nil {
		return a.fail("could not store token: %v", err)
	}

	login := userLogin(user)
	if jsonOut {
		out := map[string]any{
			"status": "complete",
			"api":    apiURL,
			"config": path,
		}
		if login != "" {
			out["user"] = login
		}
		if err := a.writeJSON(out); err != nil {
			return a.fail("%v", err)
		}
		return 0
	}

	if login != "" {
		a.info("Logged in as %s.", login)
	} else {
		a.info("Logged in.")
	}
	a.info("Token stored in %s (mode 0600).", path)
	a.info("Run `unkindled status` to see your workstations.")
	return 0
}

func userLogin(user map[string]any) string {
	for _, key := range []string{"login", "username", "name", "email"} {
		if value, ok := user[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func clampInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 5 * time.Second
	}
	if interval < time.Second {
		return time.Second
	}
	if interval > 60*time.Second {
		return 60 * time.Second
	}
	return interval
}
