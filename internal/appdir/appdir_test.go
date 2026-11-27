package appdir

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses os.UserConfigDir, not XDG_CONFIG_HOME or ~/.config")
	}
	home := t.TempDir()
	xdg := t.TempDir()
	for _, tc := range []struct {
		name string
		xdg  string
		want string
	}{
		{"XDG_CONFIG_HOME set", xdg, filepath.Join(xdg, "weblinks")},
		{"XDG_CONFIG_HOME unset", "", filepath.Join(home, ".config", "weblinks")},
		// The XDG specification says a relative value is invalid and must be
		// ignored.
		{"XDG_CONFIG_HOME relative", "relative/config", filepath.Join(home, ".config", "weblinks")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", tc.xdg)
			got, err := Dir()
			if err != nil {
				t.Fatalf("Dir: %v", err)
			}
			if got != tc.want {
				t.Errorf("Dir = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCreateIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not use Unix permission bits")
	}
	dir := filepath.Join(t.TempDir(), "config", "weblinks")
	if err := Create(dir); err != nil {
		t.Fatalf("Create: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != dirPerm {
		t.Errorf("mode = %o, want %o", perm, dirPerm)
	}
}

func TestSeedWritesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "classification.md")
	wrote, err := Seed(path, []byte("default"))
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if !wrote {
		t.Error("Seed reported no write for a missing file")
	}
	if got, _ := os.ReadFile(path); string(got) != "default" {
		t.Errorf("content = %q, want %q", got, "default")
	}
}

func TestSeedKeepsExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "classification.md")
	if err := os.WriteFile(path, []byte("edited"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	wrote, err := Seed(path, []byte("default"))
	if err != nil {
		t.Fatalf("Seed: %v", err)
	}
	if wrote {
		t.Error("Seed reported a write over an existing file")
	}
	if got, _ := os.ReadFile(path); string(got) != "edited" {
		t.Errorf("content = %q, want the user's edit kept", got)
	}
}
