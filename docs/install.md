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

`weblinks -install-agent` installs a per-user LaunchAgent that starts the
server at GUI login and restarts it if it exits. It works however you
installed the binary, and exits once the agent is loaded:

```
weblinks -install-agent
weblinks -install-agent -addr 127.0.0.1:9000   # a different address
weblinks -uninstall-agent
```

It writes `~/Library/LaunchAgents/com.jftuga.weblinks.plist` and loads it,
replacing an agent that is already loaded, so running it again is how you
change the settings later. Only `-addr`, `-dir` and `-allow-host` can be
combined with it. They are written into the plist, and each run starts from
the defaults, so pass every one of them again. The agent keeps its store,
prompt and `weblinks.log` in the [data directory](#the-data-directory), which
is resolved when you run `-install-agent` and written into the plist as an
absolute `-dir`. launchd does not see your shell's environment, so run it
again if you later change `XDG_CONFIG_HOME`. Edits to the classification file
need no reinstall or restart.

The agent runs the binary you invoked, by the path you invoked it with:
`$(brew --prefix)/bin/weblinks` for Homebrew, or the absolute path of
`./weblinks` in a clone or an unpacked release archive. That file must stay
where it is. A `go run` build and Homebrew's versioned copy under `Caskroom/`
are refused, because both disappear. Before the running agent is replaced,
the new plist is checked with `plutil -lint`, and the install stops if
something else already holds the address.

`-uninstall-agent` stops the agent and deletes its plist, and leaves the data
directory alone.

| Task | Command |
| --- | --- |
| Status | `launchctl print gui/$(id -u)/com.jftuga.weblinks` |
| Restart | `launchctl kickstart -k gui/$(id -u)/com.jftuga.weblinks` |
| Uninstall | `weblinks -uninstall-agent` |

Things to know:

- **A new binary needs a restart.** The agent keeps running the old binary
  until it restarts. In a clone, run `make build`, then `kickstart -k`.
  `brew upgrade` restarts a loaded agent for you; if it is still on the old
  version, run `kickstart -k` yourself.
- **Run `weblinks -uninstall-agent` before `brew uninstall`.** A plain
  `brew uninstall` removes the binary but leaves the agent loaded, and
  launchd keeps trying to start the missing file. The cask cannot do better,
  because Homebrew runs a cask's uninstall steps on every upgrade as well.
  `brew uninstall --zap` does remove the agent, but leaves the data
  directory.
- **`KeepAlive` is unconditional.** If something else takes the port after
  the install, a server you started by hand for example, launchd restarts the
  failing server every ten seconds and the log fills with
  `address already in use`.
- **`gui/` means GUI login.** The agent is loaded into your GUI session, so
  connecting over SSH alone will not start it.
- **`bootout` alone does not uninstall.** While the plist remains in
  `~/Library/LaunchAgents/`, launchd loads it again at the next login.
  `-uninstall-agent` does both.

The log at `weblinks.log` in the data directory is appended to and never
rotated.

## Network access

The server has no authentication. Keep `-addr` on a loopback address unless
you intend to publish your weblinks to the network. What stands in for
authentication is the origin guard described in [Request guard](security.md),
which is why an unfamiliar host name needs `-allow-host`.
