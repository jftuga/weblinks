# Installing and running

The [README](../README.md#quick-start) covers the basic build and the
command-line flags.

## Make targets

| Target | Purpose |
| --- | --- |
| `make build` | Compile `./weblinks` in the current directory. |
| `make run` | Build and serve, opening a browser. Server flags go in `ARGS`. |
| `make test` | Run the test suite. |
| `make race` | Run the test suite under the race detector. |
| `make check` | Check formatting, then run `go vet` and the tests. |
| `make install` | Install the binary into `GOBIN`, or `$(go env GOPATH)/bin` when `GOBIN` is unset. |
| `make clean` | Remove the built binary, leaving the data directory alone. |
| `make help` | List every target. |

`make run` uses the [data directory](#the-data-directory) like any other run,
so it serves your real weblinks. Pass server flags in `ARGS`, for example
`make run ARGS="-dir /tmp/scratch -no-open"` to work on a throwaway store,
with its own backups and prompt, without opening a browser.

## The data directory

The server keeps everything in one directory, so the binary runs the same way
from any working directory and from any install method, including a release
archive, Homebrew and `go install`:

| Platform | Directory |
| --- | --- |
| macOS, Linux, FreeBSD | `$XDG_CONFIG_HOME/weblinks/`, or `~/.config/weblinks/` when `XDG_CONFIG_HOME` is unset |
| Windows | `%AppData%\weblinks\` |

It holds `weblinks.json`, its `backups/`, `classification.md` and the log
written by the LaunchAgent. The server creates the directory on first run,
readable only by you, and writes the built-in classification prompt into it
when none is there. That copy is yours to edit: later releases never overwrite
it, so to pick up a newer built-in prompt, delete the file and restart the
server.

`-dir` moves the whole directory. `-file` and `-classification-file` move
just one file each: a store's `backups/` always sits beside it, and an
explicitly chosen classification file is never created for you.

## Starting at login (macOS)

`launchd/install.sh` installs a per-user LaunchAgent that starts the server at
GUI login and restarts it if it exits:

```
./launchd/install.sh
WEBLINKS_ADDR=127.0.0.1:9000 ./launchd/install.sh   # override before installing
```

The script builds the binary, renders `launchd/com.jftuga.weblinks.plist.template`
with absolute paths into `~/Library/LaunchAgents/`, and bootstraps it. It is
safe to re-run, which is also how you change `WEBLINKS_ADDR` or `WEBLINKS_DIR`
afterwards; pass every override again, since each run starts from the
defaults. The agent runs the binary in this repository but keeps its store,
prompt and `weblinks.log` in the [data directory](#the-data-directory), which
the script resolves when it runs and passes as `-dir`. launchd does not see
your shell's environment, so re-run the script if you later change
`XDG_CONFIG_HOME`. Edits to the classification file need no reinstall or
restart.

| Task | Command |
| --- | --- |
| Status | `launchctl print gui/$(id -u)/com.jftuga.weblinks` |
| Restart after `make build` | `launchctl kickstart -k gui/$(id -u)/com.jftuga.weblinks` |
| Uninstall | `launchctl bootout gui/$(id -u)/com.jftuga.weblinks && rm ~/Library/LaunchAgents/com.jftuga.weblinks.plist` |

Four things to know:

- **Code changes need a rebuild.** `make build`, then `kickstart -k`. The
  LaunchAgent environment has a minimal `PATH` without the Go toolchain, which
  is why the script builds rather than deferring to launchd.
- **`KeepAlive` is unconditional.** If something else already holds the port,
  launchd restarts the failing server every ten seconds and the log fills with
  `address already in use`. Stop any server you started by hand first.
- **`gui/` means GUI login.** `install.sh` bootstraps the agent into your GUI
  session, so connecting over SSH alone will not start it.
- **Uninstalling needs both commands.** `bootout` only unloads the agent;
  while the plist remains in `~/Library/LaunchAgents/`, launchd loads it again
  at the next login.

The log at `weblinks.log` in the data directory is appended to and never
rotated.

## Network access

The server has no authentication. Keep `-addr` on a loopback address unless
you intend to publish your weblinks to the network. What stands in for
authentication is the origin guard described in [Request guard](security.md),
which is why an unfamiliar host name needs `-allow-host`.
