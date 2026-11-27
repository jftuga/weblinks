// Command weblinks serves a personal bookmark page backed by a JSON file.
// It parses flags, opens the store, starts the HTTP server on a loopback
// address and optionally launches a browser at it. The server has no
// authentication of any kind, so binding it to anything other than localhost
// exposes the bookmark file to the network.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jftuga/weblinks/internal/bookmarks"
	"github.com/jftuga/weblinks/internal/launch"
	"github.com/jftuga/weblinks/internal/linkcheck"
	"github.com/jftuga/weblinks/internal/web"
)

// shutdownGrace is how long in-flight requests get to finish on SIGINT.
const shutdownGrace = 5 * time.Second

// programURL is where this program lives, printed alongside the version.
const programURL = "https://github.com/jftuga/weblinks"

// version is the release this binary was built from. A build can override it
// with -ldflags "-X main.version=..." to stamp in a tag or commit.
var version = "0.4.0"

// config holds the command line options.
type config struct {
	addr               string
	file               string
	classificationFile string
	noOpen             bool
	concurrency        int
	timeout            time.Duration
	allowHosts         string
	showVersion        bool
}

func main() {
	cfg := parseFlags()
	if cfg.showVersion {
		fmt.Printf("weblinks %s\n%s\n", version, programURL)
		return
	}
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "weblinks: %v\n", err)
		os.Exit(1)
	}
}

// parseFlags reads the command line into a config.
func parseFlags() config {
	var cfg config
	flag.StringVar(&cfg.addr, "addr", "127.0.0.1:5309", "address to listen on")
	flag.StringVar(&cfg.file, "file", "weblinks.json", "path to the weblinks JSON file")
	flag.StringVar(&cfg.classificationFile, "classification-file", "classification.md", "path to the classification prompt (re-read whenever requested)")
	flag.BoolVar(&cfg.noOpen, "no-open", false, "do not launch a browser on startup")
	flag.IntVar(&cfg.concurrency, "check-concurrency", 8, "concurrent requests used by the dead-link checker")
	flag.DurationVar(&cfg.timeout, "check-timeout", 10*time.Second, "per-request timeout for the dead-link checker")
	flag.StringVar(&cfg.allowHosts, "allow-host", "", "comma-separated host names, besides localhost and IP addresses, that may reach the server")
	flag.BoolVar(&cfg.showVersion, "version", false, "print the version and exit")
	flag.Parse()
	return cfg
}

// run opens the store, serves until interrupted, then shuts down cleanly.
func run(cfg config) error {
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
		Version:            version,
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

// splitHosts turns the comma-separated -allow-host value into a list,
// discarding empty entries so a trailing comma is harmless.
func splitHosts(value string) []string {
	var hosts []string
	for _, host := range strings.Split(value, ",") {
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
