package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

// parseArgs runs parseFlags over args with a fresh flag set, restoring the
// global flag state afterwards.
func parseArgs(t *testing.T, dataDir string, args ...string) config {
	t.Helper()
	oldFlags, oldArgs := flag.CommandLine, os.Args
	t.Cleanup(func() { flag.CommandLine, os.Args = oldFlags, oldArgs })
	flag.CommandLine = flag.NewFlagSet("weblinks", flag.ContinueOnError)
	os.Args = append([]string{"weblinks"}, args...)
	return parseFlags(dataDir)
}

func TestFileDefaultsFollowDataDir(t *testing.T) {
	defaultDir := filepath.Join("home", ".config", "weblinks")
	otherDir := filepath.Join("srv", "weblinks")
	for _, tc := range []struct {
		name string
		args []string
		dir  string
	}{
		{"default dir", nil, defaultDir},
		{"-dir", []string{"-dir", otherDir}, otherDir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := parseArgs(t, defaultDir, tc.args...)
			if cfg.dataDir != tc.dir {
				t.Errorf("data dir = %q, want %q", cfg.dataDir, tc.dir)
			}
			if want := filepath.Join(tc.dir, "weblinks.json"); cfg.file != want {
				t.Errorf("file = %q, want %q", cfg.file, want)
			}
			if want := filepath.Join(tc.dir, "classification.md"); cfg.classificationFile != want {
				t.Errorf("classification file = %q, want %q", cfg.classificationFile, want)
			}
			if !cfg.seedPrompt {
				t.Error("seedPrompt = false without -classification-file")
			}
		})
	}
}

func TestExplicitFileFlags(t *testing.T) {
	file, prompt := "/tmp/my store.json", "/tmp/custom prompt.md"
	cfg := parseArgs(t, "data", "-file", file, "-classification-file", prompt)
	if cfg.file != file {
		t.Errorf("file = %q, want %q", cfg.file, file)
	}
	if cfg.classificationFile != prompt {
		t.Errorf("classification file = %q, want %q", cfg.classificationFile, prompt)
	}
	if cfg.seedPrompt {
		t.Error("seedPrompt = true with an explicit -classification-file")
	}
}

func TestCheckDataDir(t *testing.T) {
	lookupErr := errors.New("$HOME is not defined")
	if err := checkDataDir("/data", lookupErr); err != nil {
		t.Errorf("an explicit -dir must not fail when the default is unknown: %v", err)
	}
	if err := checkDataDir("", lookupErr); !errors.Is(err, lookupErr) {
		t.Errorf("err = %v, want it to wrap the lookup failure", err)
	}
	if err := checkDataDir("", nil); err == nil {
		t.Error("an empty -dir was accepted")
	}
}

func TestDefaultClassificationHasPlaceholder(t *testing.T) {
	if !bytes.Contains(defaultClassification, []byte("{{sections}}")) {
		t.Error("embedded classification.md lacks the {{sections}} placeholder")
	}
}

func TestPrepareDataDirSeedsDefaultPrompt(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "weblinks")
	cfg := parseArgs(t, dataDir)
	if err := prepareDataDir(cfg); err != nil {
		t.Fatalf("prepareDataDir: %v", err)
	}
	got, err := os.ReadFile(cfg.classificationFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, defaultClassification) {
		t.Error("seeded prompt differs from the embedded default")
	}
}

func TestPrepareDataDirSkipsExplicitPrompt(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "weblinks")
	explicit := filepath.Join(root, "mine.md")
	cfg := parseArgs(t, dataDir, "-classification-file", explicit)
	if err := prepareDataDir(cfg); err != nil {
		t.Fatalf("prepareDataDir: %v", err)
	}
	for _, path := range []string{explicit, filepath.Join(dataDir, "classification.md")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s exists; an explicit -classification-file must not seed anything", path)
		}
	}
}
