// Command weblinks serves a personal bookmark page backed by a JSON file.
// It parses flags, opens the store, starts the HTTP server on a loopback
// address and optionally launches a browser at it. The store and the
// classification prompt default to files in the data directory (-dir, or the
// per-user directory from appdir), where the built-in prompt is written on
// first run. The server has no authentication of any kind, so binding it to
// anything other than localhost exposes the bookmark file to the network.
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/jftuga/weblinks/internal/appdir"
	"github.com/jftuga/weblinks/internal/bookmarks"
	"github.com/jftuga/weblinks/internal/launch"
	"github.com/jftuga/weblinks/internal/linkcheck"
	"github.com/jftuga/weblinks/internal/web"
)

// storeName and classificationName are the default file names inside the
// data directory.
const (
	storeName          = "weblinks.json"
	classificationName = "classification.md"
)

// defaultClassification is the generic prompt written to the data directory
// when no classification file exists there yet.
//
//go:embed classification.md
var defaultClassification []byte

// shutdownGrace is how long in-flight requests get to finish on SIGINT.
const shutdownGrace = 5 * time.Second

// programURL is where this program lives, printed alongside the version.
const programURL = "https://github.com/jftuga/weblinks"

// devVersion is the placeholder version for a build that was not stamped.
const devVersion = "dev"

// version is the release this binary was built from. Release builds stamp it
// from the git tag with -ldflags "-X main.version=..."; see resolveVersion for
// the fallback used by unstamped builds.
var version = devVersion

// config holds the command line options, with file paths already resolved
// against the data directory.
type config struct {
	dataDir            string
	addr               string
	file               string
	classificationFile string
	// seedPrompt is true when -classification-file was not given, so the
	// built-in prompt may be written to the data directory.
	seedPrompt  bool
	noOpen      bool
	concurrency int
	timeout     time.Duration
	allowHosts  string
	showVersion bool
}

func main() {
	// Failing to find the default directory matters only when -dir is not
	// given, so the error waits for checkDataDir instead of exiting here.
	defaultDir, dirErr := appdir.Dir()
	cfg := parseFlags(defaultDir)
	if cfg.showVersion {
		fmt.Printf("weblinks %s\n%s\n", resolveVersion(), programURL)
		return
	}
	exitOnError(checkDataDir(cfg.dataDir, dirErr))
	exitOnError(run(cfg))
}

// checkDataDir rejects an empty data directory, reporting defaultErr as the
// cause when the default could not be determined.
func checkDataDir(dir string, defaultErr error) error {
	switch {
	case dir != "":
		return nil
	case defaultErr != nil:
		return fmt.Errorf("%w; choose a data directory with -dir", defaultErr)
	default:
		return errors.New("-dir must not be empty")
	}
}

// exitOnError prints a non-nil err and exits with status 1.
func exitOnError(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "weblinks: %v\n", err)
		os.Exit(1)
	}
}

// resolveVersion returns the ldflags-stamped version when there is one, and
// otherwise the module version the Go toolchain embeds: the tag for
// `go install ...@vX.Y.Z`, or a VCS pseudo-version for a build from a git
// checkout. It returns devVersion when neither is available, as with go run.
func resolveVersion() string {
	if version != devVersion {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return devVersion
}

// parseFlags reads the command line into a config. -file and
// -classification-file default to files in -dir, so they are resolved after
// parsing, once -dir is known.
func parseFlags(defaultDir string) config {
	var cfg config
	flag.StringVar(&cfg.dataDir, "dir", defaultDir, "data directory holding the store, its backups and the classification prompt")
	flag.StringVar(&cfg.addr, "addr", "127.0.0.1:5309", "address to listen on")
	flag.StringVar(&cfg.file, "file", "", "path to the weblinks JSON file (default "+storeName+" in -dir)")
	flag.StringVar(&cfg.classificationFile, "classification-file", "", "path to the classification prompt, re-read whenever requested (default "+classificationName+" in -dir)")
	flag.BoolVar(&cfg.noOpen, "no-open", false, "do not launch a browser on startup")
	flag.IntVar(&cfg.concurrency, "check-concurrency", 8, "concurrent requests used by the dead-link checker")
	flag.DurationVar(&cfg.timeout, "check-timeout", 10*time.Second, "per-request timeout for the dead-link checker")
	flag.StringVar(&cfg.allowHosts, "allow-host", "", "comma-separated host names, besides localhost and IP addresses, that may reach the server")
	flag.BoolVar(&cfg.showVersion, "version", false, "print the version and exit")
	flag.Parse()

	if cfg.file == "" {
		cfg.file = filepath.Join(cfg.dataDir, storeName)
	}
	if cfg.classificationFile == "" {
		cfg.classificationFile = filepath.Join(cfg.dataDir, classificationName)
		cfg.seedPrompt = true
	}
	return cfg
}

// run prepares the data directory, opens the store, serves until
// interrupted, then shuts down cleanly.
func run(cfg config) error {
	if err := prepareDataDir(cfg); err != nil {
		return err
	}
	store, err := bookmarks.Open(cfg.file)
	if err != nil {
		return err
	}

	checker := linkcheck.New(linkcheck.Options{
		Concurrency: cfg.concurrency,
		Timeout:     cfg.timeout,
	})

	baseURL := "http://" + browsableAddr(cfg.addr)
	server, err := web.NewServer(store, checker, web.Options{
		BaseURL:            baseURL,
		AllowHosts:         splitHosts(cfg.allowHosts),
		Version:            resolveVersion(),
		ProgramURL:         programURL,
		ClassificationFile: cfg.classificationFile,
	})
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.addr, err)
	}

	httpServer := &http.Server{
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// Close keep-alive connections a client has abandoned. This is well
		// above browsers' own idle limits, so a browser always drops a
		// connection before the server does. There is no WriteTimeout because
		// a dead-link sweep can outlast any sensible value.
		IdleTimeout: 10 * time.Minute,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errs := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	log.Printf("serving %s at %s", store.Path(), baseURL)
	if !cfg.noOpen {
		// A failure here is never fatal: the address is already logged, so
		// the user can open it themselves.
		if err := launch.Open(baseURL); err != nil {
			log.Printf("could not open a browser: %v", err)
		}
	}

	select {
	case err := <-errs:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	log.Print("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// prepareDataDir creates the data directory and, when -classification-file
// was not given and the default file does not exist yet, writes the built-in
// prompt there. An explicitly chosen classification file is never created, so
// a mistyped path still produces the server's startup warning.
func prepareDataDir(cfg config) error {
	if err := appdir.Create(cfg.dataDir); err != nil {
		return err
	}
	if !cfg.seedPrompt {
		return nil
	}
	wrote, err := appdir.Seed(cfg.classificationFile, defaultClassification)
	if err != nil {
		return err
	}
	if wrote {
		log.Printf("wrote the default classification prompt to %s", cfg.classificationFile)
	}
	return nil
}

// splitHosts turns the comma-separated -allow-host value into a list,
// discarding empty entries so a trailing comma is harmless.
func splitHosts(value string) []string {
	var hosts []string
	for host := range strings.SplitSeq(value, ",") {
		if host = strings.TrimSpace(host); host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// browsableAddr turns a listen address into one a browser can reach,
// substituting a loopback host for a wildcard or empty one.
func browsableAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}
