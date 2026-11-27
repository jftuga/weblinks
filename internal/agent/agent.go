// Package agent installs and removes the macOS LaunchAgent that starts
// weblinks at login. It renders an embedded plist template with absolute,
// XML-escaped paths, checks the result with plutil and loads it into the
// user's GUI launchd domain with launchctl. Both entry points are macOS only
// and refuse to run as root, returning an error before running a command.
package agent

import (
	"bytes"
	_ "embed"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
	"time"
	"unicode/utf8"

	"github.com/jftuga/weblinks/internal/appdir"
)

// Label identifies the agent to launchd and names its plist.
const Label = "com.jftuga.weblinks"

// logName is the file in the data directory that receives the server's
// standard output and standard error.
const logName = "weblinks.log"

// Absolute paths, so the commands run do not depend on $PATH.
const (
	launchctl = "/bin/launchctl"
	plutil    = "/usr/bin/plutil"
)

// unloadTimeout bounds the wait for launchd to finish removing a service
// after bootout. The server allows in-flight requests five seconds to finish
// on SIGTERM, so this leaves room for that.
const unloadTimeout = 10 * time.Second

//go:embed launchagent.plist.tmpl
var plistSource string

// plistTemplate passes every substituted value through xml, so a path
// containing &, < or quotes cannot break the document.
var plistTemplate = template.Must(template.New("plist").
	Funcs(template.FuncMap{"xml": escapeXML}).
	Parse(plistSource))

// Options are the server settings written into the agent's plist.
type Options struct {
	// Addr is the -addr value, a host:port pair.
	Addr string
	// DataDir is the -dir value. It must be absolute, because launchd has no
	// working directory to resolve a relative path against.
	DataDir string
	// AllowHosts is the -allow-host value, left out of the plist when empty.
	AllowHosts string
}

// validate rejects options the agent could not run with.
func (o Options) validate() error {
	if !filepath.IsAbs(o.DataDir) {
		return fmt.Errorf("the data directory must be an absolute path, got %q", o.DataDir)
	}
	if _, _, err := net.SplitHostPort(o.Addr); err != nil {
		return fmt.Errorf("invalid -addr %q: %w", o.Addr, err)
	}
	return nil
}

// Install writes the LaunchAgent plist for the running binary and opts, and
// loads it, replacing an agent that is already loaded. The data directory is
// created first, since launchd opens the log and enters the directory before
// the binary starts.
func Install(opts Options) error {
	return install(runtime.GOOS, opts)
}

// Uninstall unloads the agent if it is loaded and deletes its plist. The data
// directory is never touched.
func Uninstall() error {
	return uninstall(runtime.GOOS)
}

// install implements Install for the given GOOS. Everything that can fail
// without side effects is checked before an existing agent is stopped, so a
// failed install leaves the old agent running where possible.
func install(goos string, opts Options) error {
	if err := checkEnvironment(goos); err != nil {
		return err
	}
	if err := opts.validate(); err != nil {
		return err
	}
	// On macOS os.Executable returns the path the binary was started by,
	// made absolute, without resolving symlinks (verified on macOS 26 with Go
	// 1.27, both for a path and for a lookup through $PATH). For a Homebrew
	// install that is the stable $(brew --prefix)/bin/weblinks symlink rather
	// than the versioned Caskroom copy that brew upgrade deletes.
	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate the weblinks binary: %w", err)
	}
	if err := checkBinary(binary); err != nil {
		return err
	}
	plist, err := plistPath()
	if err != nil {
		return err
	}
	if err := appdir.Create(opts.DataDir); err != nil {
		return err
	}
	candidate, err := writeCandidate(plist, binary, opts)
	if err != nil {
		return err
	}
	// After the rename below this fails harmlessly, as nothing is left.
	defer os.Remove(candidate)

	stopped := isLoaded()
	if stopped {
		if err := unload(); err != nil {
			return err
		}
	}
	if err := checkAddrFree(opts.Addr); err != nil {
		if stopped {
			return fmt.Errorf("%w; the previous agent was stopped and its plist left in place, start it again with: launchctl bootstrap %s %s", err, domain(), plist)
		}
		return err
	}
	if err := os.Rename(candidate, plist); err != nil {
		return fmt.Errorf("install %s: %w", plist, err)
	}
	if err := runCommand(launchctl, "bootstrap", domain(), plist); err != nil {
		return err
	}

	fmt.Printf("installed  %s\n", plist)
	fmt.Printf("binary     %s (the agent runs this file, so it must stay in place)\n", binary)
	fmt.Printf("address    %s\n", opts.Addr)
	fmt.Printf("log        %s\n", logPath(opts.DataDir))
	fmt.Println()
	fmt.Printf("status:    launchctl print %s\n", service())
	fmt.Printf("restart:   launchctl kickstart -k %s\n", service())
	fmt.Println("remove:    weblinks -uninstall-agent")
	return nil
}

// uninstall implements Uninstall for the given GOOS.
func uninstall(goos string) error {
	if err := checkEnvironment(goos); err != nil {
		return err
	}
	plist, err := plistPath()
	if err != nil {
		return err
	}
	// bootout's exit status does not reliably tell "not loaded" apart from a
	// real failure, so print decides whether there is anything to stop.
	if isLoaded() {
		if err := unload(); err != nil {
			return err
		}
		fmt.Printf("stopped    %s\n", service())
	} else {
		fmt.Printf("not loaded %s\n", service())
	}
	switch err := os.Remove(plist); {
	case err == nil:
		fmt.Printf("removed    %s\n", plist)
	case errors.Is(err, fs.ErrNotExist):
		fmt.Printf("no plist   %s\n", plist)
	default:
		return fmt.Errorf("remove %s: %w", plist, err)
	}
	fmt.Println("the data directory was left in place")
	return nil
}

// Render returns the plist that runs binary with opts.
func Render(binary string, opts Options) ([]byte, error) {
	data := struct {
		Options
		Label, Binary, Log string
	}{opts, Label, binary, logPath(opts.DataDir)}
	var buf bytes.Buffer
	if err := plistTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render the plist: %w", err)
	}
	return buf.Bytes(), nil
}

// escapeXML escapes s for use as XML character data. It rejects a value
// holding characters XML cannot represent, such as control characters or
// invalid UTF-8, which xml.EscapeText would silently replace with U+FFFD and
// so point the agent at the wrong path.
func escapeXML(s string) (string, error) {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		return "", err
	}
	if strings.ContainsRune(b.String(), utf8.RuneError) {
		return "", fmt.Errorf("%q contains characters a plist cannot hold", s)
	}
	return b.String(), nil
}

// checkBinary rejects a binary that will not stay where the agent expects
// it: a go run build in a temporary go-build directory, or a Homebrew cask's
// versioned copy under Caskroom, which brew upgrade deletes.
func checkBinary(path string) error {
	for part := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		if isGoBuildDir(part) {
			return fmt.Errorf("%s is a temporary go run build; build or install the binary first", path)
		}
		if part == "Caskroom" {
			return fmt.Errorf("%s is a versioned copy that brew upgrade deletes; run the weblinks on your PATH instead, usually $(brew --prefix)/bin/weblinks", path)
		}
	}
	return nil
}

// isGoBuildDir reports whether name is a go command work directory, which is
// "go-build" followed by digits.
func isGoBuildDir(name string) bool {
	digits, ok := strings.CutPrefix(name, "go-build")
	return ok && digits != "" && strings.Trim(digits, "0123456789") == ""
}

// writeCandidate renders the plist into a temporary file next to its final
// path and lints it, returning the temporary file's name. The name does not
// end in .plist, so launchd never picks the file up.
func writeCandidate(plist, binary string, opts Options) (string, error) {
	content, err := Render(binary, opts)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(plist)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, "."+Label+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("create a temporary plist: %w", err)
	}
	name := f.Name()
	_, writeErr := f.Write(content)
	err = errors.Join(writeErr, f.Close(), os.Chmod(name, 0o644))
	if err == nil {
		err = runCommand(plutil, "-lint", "-s", name)
	}
	if err != nil {
		os.Remove(name)
		return "", fmt.Errorf("write the plist: %w", err)
	}
	return name, nil
}

// checkAddrFree fails when addr cannot be listened on. KeepAlive would
// otherwise restart the failing server every ten seconds and fill the log
// with "address already in use".
func checkAddrFree(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("%s is not available, free it or choose another -addr: %w", addr, err)
	}
	return l.Close()
}

// isLoaded reports whether launchd knows the agent.
func isLoaded() bool {
	return exec.Command(launchctl, "print", service()).Run() == nil
}

// unload boots the agent out and waits until launchd has removed it.
// bootout returns before the service is gone, and a bootstrap that lands
// while it still exists fails with "5: Input/output error". bootout can
// report an error even when it succeeds, so whether the service is still
// loaded decides the outcome, and bootout's error is only reported with it.
func unload() error {
	bootoutErr := runCommand(launchctl, "bootout", service())
	deadline := time.Now().Add(unloadTimeout)
	for isLoaded() {
		if time.Now().After(deadline) {
			return errors.Join(fmt.Errorf("%s is still loaded %s after bootout", service(), unloadTimeout), bootoutErr)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// runCommand runs name with args, folding its output into any error.
func runCommand(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", filepath.Base(name), strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}

// checkEnvironment fails on any system other than macOS, and when running as
// root. The agent belongs to the user's GUI session, and under sudo the data
// directory and plist would be created as root in the user's home, where the
// server could no longer write them.
func checkEnvironment(goos string) error {
	if goos != "darwin" {
		return fmt.Errorf("LaunchAgents are macOS only, and this is %s", goos)
	}
	if os.Geteuid() == 0 {
		return errors.New("run this as yourself, not as root or with sudo: the agent belongs to your login session")
	}
	return nil
}

// plistPath returns where the agent's plist is installed.
func plistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate the home directory: %w", err)
	}
	return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
}

// logPath returns the log file inside dataDir.
func logPath(dataDir string) string {
	return filepath.Join(dataDir, logName)
}

// domain returns the launchd domain of the current user's GUI session.
func domain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

// service returns the launchd service target of the agent.
func service() string {
	return domain() + "/" + Label
}
