# unkindled CLI

A small, zero-dependency Go CLI for [Unkindled](https://unkindled.dev). It signs
you in with GitHub's device flow, keeps your local SSH setup in sync with the
control plane, and registers your workstations with
[Herdr](https://herdr.dev).

## Install

Requires Go 1.22 or newer.

```sh
go install github.com/arthuRHD/unkindled-cli/cmd/unkindled@latest
```

Or build from a checkout:

```sh
make build
./bin/unkindled version
```

## Sign in

```sh
unkindled login
```

The CLI starts a GitHub device flow and prints a verification URL and a
one-time code. Open the URL in a browser, enter the code, and return to the
terminal. When GitHub confirms the sign-in, the CLI stores the API token and
prints the config path.

```text
To sign in, open https://github.com/login/device
and enter code: ABCD-1234
Waiting for authorization...
Logged in as you.
Token stored in /home/you/.config/unkindled/config.json (mode 0600).
```

The token is never printed. The config file is written with mode `0600`.

For non-interactive environments, pass a token directly:

```sh
unkindled login --token "$UNKINDLED_TOKEN"
```

Use `--api URL` to target another control plane (for example a staging
deployment). The default is `https://api.unkindled.dev`. Add `--json` for
newline-delimited machine-readable events; the JSON output never includes the
token.

Config path:

1. `$XDG_CONFIG_HOME/unkindled/config.json` when `XDG_CONFIG_HOME` is an
   absolute path
2. otherwise `~/.config/unkindled/config.json`

## Status

```sh
unkindled status
unkindled workstations   # alias
unkindled status --json
```

Status lists each workstation's name, state, plan, and IPv4 address, and checks
the local setup:

- a managed `# unkindled:<id>` block exists in `~/.ssh/config`, or an equivalent
  hand-written `Host` block matches the API alias, host, user and identity file
- the block's host alias and identity file match the API connection details
- the private key exists and is mode `0600` when the platform generated it
- `herdr machine list --json` contains the workstation

A summary calls out workstations that still need configuration and prints the
next command to run. If Herdr is not installed, status reports that and skips
the Herdr checks rather than failing.

## SSH sync

```sh
unkindled ssh sync
unkindled ssh sync --dry-run   # show the diff and key actions only
unkindled ssh sync --yes       # skip the confirmation prompt
```

For every workstation, the CLI fetches
`/v1/workstations/{id}/connection`. If the platform generated the SSH key and
no local key exists yet, it calls the reveal endpoint once and writes the key to
`~/.ssh/unkindled/<safe-name>` with mode `0600` and a `0700` parent directory.
Key material is never printed.

It then adds or replaces this managed block in `~/.ssh/config`:

```text
# unkindled:<id>
Host <alias>
  HostName <ipv4>
  User <user>
  IdentityFile <key path>
  IdentitiesOnly yes
# end unkindled:<id>
```

A non-default SSH port is written as a `Port` line before `IdentityFile`.
Unrelated hosts, comments, and ordering are preserved. The identity path
returned by the API is authoritative, so an existing platform-generated key is
reused even when it is not under `~/.ssh/unkindled/`.

If an equivalent hand-written `Host` block already exists, `ssh sync` leaves it
unchanged instead of appending a duplicate alias. A hand-written block with the
same alias but different host, user, port or identity is reported as a conflict
and is not overwritten. Running the command again is idempotent; existing keys
are reused without another reveal call.
`~/.ssh` is created with mode `0700` when needed and the config is written with
mode `0600`.

Workstation names are converted to safe file names. Names containing path
separators or control characters are rejected, so API data cannot escape
`~/.ssh/unkindled`.

## Herdr sync

```sh
unkindled herdr sync
unkindled herdr sync --print     # print commands, do not run them
unkindled herdr sync --dry-run   # show commands that would run
unkindled herdr sync --yes       # skip the confirmation prompt
```

For each workstation missing from `herdr machine list --json`, the CLI runs:

```text
herdr machine add <ssh-alias> --label <workstation-name> --remote-session default
```

The SSH alias matches the block written by `unkindled ssh sync`. Run that first
if you have not configured SSH yet.

If Herdr is not installed, the CLI prints an install hint. A requested sync
exits non-zero; `--print` and `--dry-run` remain informational and exit zero.

## Doctor

```sh
unkindled doctor
```

Doctor prints `PASS`, `WARN`, and `FAIL` lines for:

- config file presence and permissions
- stored API token
- API reachability and token validity
- `~/.ssh` and `~/.ssh/config`
- managed SSH blocks and private key permissions
- Herdr availability and its machine list

It exits non-zero when a check fails and prints a suggested next command.

## Log out

```sh
unkindled logout
```

This deletes the local config and token. The CLI does not revoke the token
server-side, so it remains valid until it expires. The command prints this note.

## Security notes

- Treat the API token as a secret. It is stored only in
  `config.json` with mode `0600` and is never printed by the CLI.
- The device flow writes the token only after authorization completes. Pending,
  `slow_down`, expired, denied, and network failures produce actionable errors
  and do not store a token.
- Platform-generated private keys are written once to
  `~/.ssh/unkindled/<safe-name>` with mode `0600`; key contents are never shown
  in output, including errors and dry runs.
- `ssh sync --dry-run` is safe: it calls no reveal endpoint and changes no
  files.
- Managed blocks are delimited by `# unkindled:<id>` and
  `# end unkindled:<id>`. The editor refuses to modify malformed or duplicate
  blocks instead of guessing.
- The CLI never modifies unrelated SSH config entries or external identity
  files.
- Review the confirmation prompt or use `--dry-run` before applying changes to
  shared dotfiles. Do not commit private keys.

## Development

```sh
make fmt    # gofmt
make vet    # go vet
make test   # go test ./...
make build  # binary in bin/unkindled
```

The test suite uses `httptest` servers, temporary HOME/XDG directories, and a
fake `herdr` executable. No real API, SSH host, or Herdr installation is
required.

## License

MIT. See [LICENSE](LICENSE).
