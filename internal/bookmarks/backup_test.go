package bookmarks

import (
	"errors"
	"path/filepath"
	"testing"
)

// newBackupStore returns a store holding one bookmark, which is enough for a
// first backup to exist the moment anything else changes.
func newBackupStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "weblinks.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := store.Add(LinkInput{URL: "https://example.com/one", Title: "One", Section: "Main"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	return store
}

func TestRestoreUndoesADelete(t *testing.T) {
	store := newBackupStore(t)
	second, err := store.Add(LinkInput{URL: "https://example.com/two", Title: "Two", Section: "Main"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Delete(second.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	backups, err := store.Backups()
	if err != nil {
		t.Fatalf("Backups: %v", err)
	}
	// Newest first: the delete's backup holds both links, so restoring it
	// brings the deleted bookmark back.
	if len(backups) == 0 {
		t.Fatal("expected at least one backup")
	}
	if backups[0].Links != 2 || backups[0].Sections != 1 {
		t.Fatalf("newest backup has %d links in %d sections, want 2 in 1",
			backups[0].Links, backups[0].Sections)
	}
	if backups[0].Taken.IsZero() {
		t.Error("backup timestamp was not recovered from the file name")
	}

	if err := store.Restore(backups[0].Name); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if _, err := store.Lookup("https://example.com/two"); err != nil {
		t.Errorf("the deleted bookmark did not come back: %v", err)
	}
}

func TestRestoreIsItselfUndoable(t *testing.T) {
	store := newBackupStore(t)
	if _, err := store.Add(LinkInput{URL: "https://example.com/two", Title: "Two", Section: "Main"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	backups, err := store.Backups()
	if err != nil {
		t.Fatalf("Backups: %v", err)
	}
	oldest := backups[len(backups)-1]

	if err := store.Restore(oldest.Name); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	// The document that was replaced must have been captured on the way past.
	after, err := store.Backups()
	if err != nil {
		t.Fatalf("Backups: %v", err)
	}
	if len(after) <= len(backups) {
		t.Fatalf("restore took no backup: %d before, %d after", len(backups), len(after))
	}
	if after[0].Links != 2 {
		t.Errorf("the replaced document was not the one backed up: %d links", after[0].Links)
	}
}

func TestRestoreRejectsBadNames(t *testing.T) {
	store := newBackupStore(t)
	if _, err := store.Add(LinkInput{URL: "https://example.com/two", Title: "Two", Section: "Main"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	for name, backup := range map[string]string{
		"escape upwards":  "../weblinks.json",
		"absolute path":   "/etc/passwd",
		"nested path":     "sub/weblinks-x.json",
		"wrong prefix":    "other.json",
		"empty":           "",
		"wrong extension": "weblinks-20260101T000000.000000000Z.txt",
	} {
		t.Run(name, func(t *testing.T) {
			if err := store.Restore(backup); err == nil {
				t.Errorf("Restore(%q) was accepted", backup)
			}
		})
	}

	err := store.Restore("weblinks-20260101T000000.000000000Z.json")
	if !errors.Is(err, ErrBackupNotFound) {
		t.Errorf("missing backup: err = %v, want ErrBackupNotFound", err)
	}
}
