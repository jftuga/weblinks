# Installing and running

The [README](../README.md#quick-start) covers the basic build and the
command-line flags.

## Make targets

| Target | Purpose |
| --- | --- |
| `make build` | Compile `./weblinks` in the current directory. |
| `make run` | Build and serve, opening a browser. |
| `make serve` | Build and serve without opening a browser. |
| `make daemon` | Build, then serve detached with `nohup` so it outlives the shell. Logs to `weblinks.log`, or `LOG=`. |
| `make install` | Install the binary into `GOBIN`, or `$(go env GOPATH)/bin` when `GOBIN` is unset. |
| `make help` | List every target. |

`run`, `serve` and `daemon` accept `ADDR=`, `FILE=` and `CLASSIFICATION_FILE=`
overrides, for example `make serve ADDR=127.0.0.1:9000`.

## Running outside the repository

Relative file paths are resolved from the server's working directory. If you
run the binary from anywhere else, including after `make install`, also copy
`classification.md` from this repository and either run from its directory or
set `-classification-file`:

```
weblinks -classification-file /path/to/classification.md
```

The same applies to `-file`: without it, `weblinks.json` and its `backups/`
directory are read from and written to whatever directory you start the
server from.

## Starting at login (macOS)

`launchd/install.sh` installs a per-user LaunchAgent that starts the server at
GUI login and restarts it if it exits:

```
./launchd/install.sh
ADDR=127.0.0.1:9000 ./launchd/install.sh   # override before installing
```

The script builds the binary, renders `launchd/com.jftuga.weblinks.plist.template`
with absolute paths into `~/Library/LaunchAgents/`, and bootstraps it. It is
safe to re-run, which is also how you change `ADDR`, `FILE`,
`CLASSIFICATION_FILE` or `LOG` afterwards; pass every override again, since
each run starts from the defaults. The classification file defaults to the
repository's `classification.md`; edits to its contents need no reinstall or
restart.

| Task | Command |
| --- | --- |
| Status | `launchctl print gui/$(id -u)/com.jftuga.weblinks` |
| Restart after `make build` | `launchctl kickstart -k gui/$(id -u)/com.jftuga.weblinks` |
| Uninstall | `launchctl bootout gui/$(id -u)/com.jftuga.weblinks && rm ~/Library/LaunchAgents/com.jftuga.weblinks.plist` |

Five things to know:

- **The agent runs the compiled binary, not `make daemon`.** launchd is itself
  the supervisor and expects to `exec` a process that stays in the foreground.
  `make daemon` forks and exits immediately, so launchd would lose track of the
  real server and respawn it forever.
- **Code changes need a rebuild.** `make build`, then `kickstart -k`. The
  LaunchAgent environment has a minimal `PATH` without the Go toolchain, which
  is why the script builds rather than deferring to launchd.
- **`KeepAlive` is unconditional.** If something else already holds the port,
  launchd restarts the failing server every ten seconds and the log fills with
  `address already in use`. Check for a stray `make daemon` process first.
- **`gui/` means GUI login.** `install.sh` bootstraps the agent into your GUI
  session, so connecting over SSH alone will not start it.
- **Uninstalling needs both commands.** `bootout` only unloads the agent;
  while the plist remains in `~/Library/LaunchAgents/`, launchd loads it again
  at the next login.

The log at `weblinks.log` is appended to and never rotated.

## Network access

The server has no authentication. Keep `-addr` on a loopback address unless
you intend to publish your weblinks to the network. What stands in for
authentication is the origin guard described in [Request guard](security.md),
which is why an unfamiliar host name needs `-allow-host`.
