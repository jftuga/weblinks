#!/usr/bin/env bash
#
# Installs weblinks as a per-user LaunchAgent so it starts at GUI login.
# The plist template is rendered with absolute paths, the binary from this
# repository and its data in the per-user data directory, then bootstrapped
# into the caller's GUI domain. Re-running the script is safe: an
# already-loaded agent is booted out first, so this doubles as the upgrade
# path after changing configuration.

set -euo pipefail

LABEL="com.jftuga.weblinks"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# WEBLINKS_DIR and WEBLINKS_ADDR override the defaults. The data directory
# defaults to the one the binary would choose (internal/appdir) and is passed
# to it as -dir: launchd does not see the shell's environment, so passing it
# explicitly is what keeps the agent on the same directory.
DATA_DIR="${WEBLINKS_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/weblinks}"
ADDR="${WEBLINKS_ADDR:-127.0.0.1:5309}"
LOG="$DATA_DIR/weblinks.log"

# launchd has no meaningful working directory to resolve a relative path
# against, so the directory must be absolute.
if [[ "$DATA_DIR" != /* ]]; then
    echo "error: the data directory must be an absolute path, got: $DATA_DIR (check WEBLINKS_DIR and XDG_CONFIG_HOME)" >&2
    exit 1
fi

BINARY="$REPO/weblinks"
TEMPLATE="$REPO/launchd/$LABEL.plist.template"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
DOMAIN="gui/$(id -u)"

if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "error: launchd agents are macOS only" >&2
    exit 1
fi

if [[ ! -f "$TEMPLATE" ]]; then
    echo "error: missing template: $TEMPLATE" >&2
    exit 1
fi

# launchd execs the binary directly with a minimal PATH that does not
# include the Go toolchain, so the build has to happen here instead.
echo "building $BINARY"
make -C "$REPO" build

mkdir -p "$HOME/Library/LaunchAgents"

# launchd opens the log and enters the working directory before it starts the
# binary, which would otherwise create the data directory itself.
mkdir -p -m 700 "$DATA_DIR"

# | is the substitution delimiter because every replacement is a path.
sed -e "s|{{LABEL}}|$LABEL|g" \
    -e "s|{{BINARY}}|$BINARY|g" \
    -e "s|{{ADDR}}|$ADDR|g" \
    -e "s|{{DATA_DIR}}|$DATA_DIR|g" \
    -e "s|{{LOG}}|$LOG|g" \
    "$TEMPLATE" >| "$PLIST"

plutil -lint "$PLIST" >/dev/null

# bootout fails when nothing is loaded, which is the normal first-run case.
launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null || true

# bootout returns before launchd has finished removing the service, and a
# bootstrap that lands while it still exists fails with "5: Input/output
# error". Poll until the service is gone, giving up after five seconds.
for _ in {1..50}; do
    launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1 || break
    sleep 0.1
done
if launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1; then
    echo "error: $LABEL is still loaded after bootout" >&2
    exit 1
fi

launchctl bootstrap "$DOMAIN" "$PLIST"

echo "installed $PLIST"
echo "serving   http://$ADDR"
echo "log       $LOG"
echo
echo "status:   launchctl print $DOMAIN/$LABEL"
echo "restart:  launchctl kickstart -k $DOMAIN/$LABEL"
echo "remove:   launchctl bootout $DOMAIN/$LABEL && rm $PLIST"
