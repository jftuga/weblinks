package agent

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// These tests never call Install or Uninstall: on a Mac they would run
// launchctl against the user's live agent.

// parsePlist maps each key of a rendered plist's top-level dict to the
// <string> values that follow it, so an array's strings collect under its key.
func parsePlist(t *testing.T, data []byte) map[string][]string {
	t.Helper()
	values := make(map[string][]string)
	dec := xml.NewDecoder(bytes.NewReader(data))
	key := ""
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return values
		}
		if err != nil {
			t.Fatalf("rendered plist is not valid XML: %v\n%s", err, data)
		}
		start, ok := tok.(xml.StartElement)
		if !ok || (start.Name.Local != "key" && start.Name.Local != "string") {
			continue
		}
		var text string
		if err := dec.DecodeElement(&text, &start); err != nil {
			t.Fatalf("decode <%s>: %v", start.Name.Local, err)
		}
		if start.Name.Local == "key" {
			key = text
		} else {
			values[key] = append(values[key], text)
		}
	}
}

// awkward holds the XML metacharacters that broke the old sed-based installer.
const awkward = `a&b <c> "d" 'e'`

func TestRenderArguments(t *testing.T) {
	binary := filepath.Join("/Users", awkward, "bin", "weblinks")
	dir := filepath.Join("/Users", awkward, ".config", "weblinks")
	data, err := Render(binary, Options{Addr: "127.0.0.1:5309", DataDir: dir})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	values := parsePlist(t, data)
	log := filepath.Join(dir, "weblinks.log")
	for key, want := range map[string][]string{
		"Label":             {Label},
		"ProgramArguments":  {binary, "-addr", "127.0.0.1:5309", "-dir", dir, "-no-open"},
		"WorkingDirectory":  {dir},
		"StandardOutPath":   {log},
		"StandardErrorPath": {log},
		"ProcessType":       {"Background"},
	} {
		if got := values[key]; !slices.Equal(got, want) {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestRenderAllowHost(t *testing.T) {
	data, err := Render("/bin/weblinks", Options{Addr: "0.0.0.0:80", DataDir: "/data", AllowHosts: "a.example,b&c.example"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := []string{"/bin/weblinks", "-addr", "0.0.0.0:80", "-dir", "/data", "-allow-host", "a.example,b&c.example", "-no-open"}
	if got := parsePlist(t, data)["ProgramArguments"]; !slices.Equal(got, want) {
		t.Errorf("ProgramArguments = %q, want %q", got, want)
	}
}

func TestRenderRejectsUnencodableValues(t *testing.T) {
	for name, dir := range map[string]string{
		"control character": "/data/a\x01b",
		"invalid UTF-8":     "/data/a\xffb",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Render("/bin/weblinks", Options{Addr: "127.0.0.1:5309", DataDir: dir}); err == nil {
				t.Errorf("Render accepted %q", dir)
			}
		})
	}
}

func TestRenderPassesPlutil(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("plutil is macOS only")
	}
	data, err := Render(filepath.Join("/", awkward, "weblinks"), Options{Addr: "127.0.0.1:5309", DataDir: filepath.Join("/", awkward), AllowHosts: awkward})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	path := filepath.Join(t.TempDir(), "test.plist")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(plutil, "-lint", path).CombinedOutput(); err != nil {
		t.Errorf("plutil -lint: %v: %s", err, out)
	}
}

func TestCheckBinary(t *testing.T) {
	for _, tc := range []struct {
		path string
		ok   bool
	}{
		{"/opt/homebrew/bin/weblinks", true},
		{"/usr/local/bin/weblinks", true},
		{"/Users/me/go/bin/weblinks", true},
		{"/Users/me/Downloads/weblinks_0.5.0_darwin_arm64/weblinks", true},
		{"/Users/me/src/go-builds/weblinks/weblinks", true},
		{"/Users/me/go-build/weblinks", true},
		{"/opt/homebrew/Caskroom/weblinks/0.5.0/weblinks", false},
		{"/var/folders/xy/T/go-build123456/b001/exe/weblinks", false},
	} {
		if err := checkBinary(tc.path); (err == nil) != tc.ok {
			t.Errorf("checkBinary(%q) = %v, want ok=%v", tc.path, err, tc.ok)
		}
	}
}

func TestOptionsValidate(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
		ok   bool
	}{
		{"valid", Options{Addr: "127.0.0.1:5309", DataDir: "/data"}, true},
		{"relative dir", Options{Addr: "127.0.0.1:5309", DataDir: "data"}, false},
		{"empty dir", Options{Addr: "127.0.0.1:5309"}, false},
		{"no port", Options{Addr: "127.0.0.1", DataDir: "/data"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.opts.validate(); (err == nil) != tc.ok {
				t.Errorf("validate() = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// TestNotMacOS passes a non-darwin GOOS, so both functions must fail before
// running any command.
func TestNotMacOS(t *testing.T) {
	opts := Options{Addr: "127.0.0.1:5309", DataDir: "/data"}
	if err := install("linux", opts); err == nil || !strings.Contains(err.Error(), "macOS only") {
		t.Errorf("install on linux = %v, want a macOS-only error", err)
	}
	if err := uninstall("windows"); err == nil || !strings.Contains(err.Error(), "macOS only") {
		t.Errorf("uninstall on windows = %v, want a macOS-only error", err)
	}
}
