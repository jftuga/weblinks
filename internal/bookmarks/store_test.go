package bookmarks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// newStore returns a Store backed by a fresh file in a temporary directory.
func newStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "weblinks.json")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return store
}

// sampleInput returns a valid LinkInput for the given URL.
func sampleInput(url, section string) LinkInput {
	return LinkInput{URL: url, Title: "Example", Notes: "note", Section: section}
}

func TestOpenCreatesMissingFile(t *testing.T) {
	store := newStore(t)
	if _, err := os.Stat(store.Path()); err != nil {
		t.Fatalf("expected store file to exist: %v", err)
	}
}

func TestSetTitle(t *testing.T) {
	store := newStore(t)
	if doc := readDoc(t, store.Path()); doc.Title != "Weblinks" {
		t.Fatalf("default title = %q, want Weblinks", doc.Title)
	}

	title, err := store.SetTitle("  My Links  ")
	if err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	if title != "My Links" {
		t.Errorf("title = %q, want the trimmed title", title)
	}
	if doc := readDoc(t, store.Path()); doc.Title != "My Links" {
		t.Errorf("stored title = %q, want My Links", doc.Title)
	}

	if _, err := store.SetTitle("   "); err == nil {
		t.Fatal("expected a blank title to be refused")
	}
	if doc := readDoc(t, store.Path()); doc.Title != "My Links" {
		t.Errorf("a refused title still wrote: %q", doc.Title)
	}
}

func TestAddCreatesSectionOnDemand(t *testing.T) {
	store := newStore(t)
	link, err := store.Add(sampleInput("https://example.com", "Main"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if link.ID == "" {
		t.Error("expected a generated ID")
	}
	if link.ClickCount != 0 || link.LastClicked != nil {
		t.Error("new link should have no click history")
	}

	doc := readDoc(t, store.Path())
	if len(doc.Sections) != 1 || doc.Sections[0].Name != "Main" {
		t.Fatalf("expected one section named Main, got %+v", doc.Sections)
	}
}

func TestAddRejectsDuplicateURL(t *testing.T) {
	store := newStore(t)
	if _, err := store.Add(sampleInput("https://example.com", "Main")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	_, err := store.Add(sampleInput("https://example.com", "Other"))
	if !errors.Is(err, ErrDuplicateURL) {
		t.Fatalf("expected ErrDuplicateURL, got %v", err)
	}
}

func TestAddRejectsInvalidInput(t *testing.T) {
	store := newStore(t)
	cases := map[string]LinkInput{
		"empty url":     {URL: "", Section: "Main"},
		"no scheme":     {URL: "example.com", Section: "Main"},
		"no section":    {URL: "https://example.com", Section: ""},
		"scheme only":   {URL: "https://", Section: "Main"},
		"file no path":  {URL: "file://", Section: "Main"},
		"file relative": {URL: "file:notes.txt", Section: "Main"},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Add(in); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestAddDefaultsTitleToURL(t *testing.T) {
	store := newStore(t)
	link, err := store.Add(LinkInput{URL: "https://example.com", Section: "Main"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if link.Title != "https://example.com" {
		t.Errorf("title = %q, want the URL", link.Title)
	}
}

func TestAddAcceptsFileURL(t *testing.T) {
	store := newStore(t)
	for _, raw := range []string{"file:///Users/me/Notes.txt", "file://localhost/tmp/a%20b.pdf"} {
		if _, err := store.Add(LinkInput{URL: raw, Section: "Main"}); err != nil {
			t.Errorf("Add(%q): %v", raw, err)
		}
	}
}

func TestUpdateMovesBetweenSectionsAndPrunes(t *testing.T) {
	store := newStore(t)
	link, err := store.Add(sampleInput("https://example.com", "Main"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	moved, err := store.Update(link.ID, sampleInput("https://example.com", "Archive"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if moved.ID != link.ID {
		t.Error("update should preserve the link ID")
	}

	doc := readDoc(t, store.Path())
	if len(doc.Sections) != 1 || doc.Sections[0].Name != "Archive" {
		t.Fatalf("expected only an Archive section, got %+v", doc.Sections)
	}
}

func TestUpdatePreservesClickHistory(t *testing.T) {
	store := newStore(t)
	link, _ := store.Add(sampleInput("https://example.com", "Main"))
	if _, err := store.RecordClick(link.ID); err != nil {
		t.Fatalf("RecordClick: %v", err)
	}

	updated, err := store.Update(link.ID, sampleInput("https://example.org", "Main"))
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.ClickCount != 1 || updated.LastClicked == nil {
		t.Error("editing a link should not reset its click history")
	}
}

func TestUpdateRejectsUnknownID(t *testing.T) {
	store := newStore(t)
	_, err := store.Update("nope", sampleInput("https://example.com", "Main"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteRemovesLinkAndEmptySection(t *testing.T) {
	store := newStore(t)
	link, _ := store.Add(sampleInput("https://example.com", "Main"))
	if err := store.Delete(link.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if doc := readDoc(t, store.Path()); len(doc.Sections) != 0 {
		t.Fatalf("expected no sections left, got %+v", doc.Sections)
	}
	if err := store.Delete(link.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on second delete, got %v", err)
	}
}

func TestRenameSectionKeepsLinksAndPosition(t *testing.T) {
	store := newStore(t)
	first, _ := store.Add(sampleInput("https://one.example", "Main"))
	if _, err := store.Add(sampleInput("https://two.example", "Archive")); err != nil {
		t.Fatalf("Add: %v", err)
	}

	name, err := store.RenameSection(SectionRename{From: "Main", To: "  Primary  "})
	if err != nil {
		t.Fatalf("RenameSection: %v", err)
	}
	if name != "Primary" {
		t.Errorf("name = %q, want the trimmed new name", name)
	}

	doc := readDoc(t, store.Path())
	if len(doc.Sections) != 2 {
		t.Fatalf("expected two sections, got %+v", doc.Sections)
	}
	// A rename must not reorder the document the way a move through Update
	// would, so the renamed section stays first.
	if doc.Sections[0].Name != "Primary" || doc.Sections[1].Name != "Archive" {
		t.Fatalf("section order changed: %q, %q", doc.Sections[0].Name, doc.Sections[1].Name)
	}
	if len(doc.Sections[0].Links) != 1 || doc.Sections[0].Links[0].ID != first.ID {
		t.Errorf("links did not survive the rename: %+v", doc.Sections[0].Links)
	}
}

func TestRenameSectionRejectsExistingName(t *testing.T) {
	store := newStore(t)
	if _, err := store.Add(sampleInput("https://one.example", "Main")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := store.Add(sampleInput("https://two.example", "Archive")); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Case-insensitively equal counts as taken, since findSection matches that
	// way and the rename would otherwise create an unreachable second section.
	if _, err := store.RenameSection(SectionRename{From: "Main", To: "archive"}); !errors.Is(err, ErrDuplicateSection) {
		t.Fatalf("expected ErrDuplicateSection, got %v", err)
	}
	if doc := readDoc(t, store.Path()); doc.Sections[0].Name != "Main" {
		t.Errorf("a refused rename still wrote: %+v", doc.Sections)
	}
}

func TestRenameSectionAllowsCaseChange(t *testing.T) {
	store := newStore(t)
	if _, err := store.Add(sampleInput("https://one.example", "github")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := store.RenameSection(SectionRename{From: "github", To: "GitHub"}); err != nil {
		t.Fatalf("RenameSection: %v", err)
	}
	doc := readDoc(t, store.Path())
	if len(doc.Sections) != 1 || doc.Sections[0].Name != "GitHub" {
		t.Fatalf("expected a single GitHub section, got %+v", doc.Sections)
	}
}

func TestRenameSectionRejectsBadInput(t *testing.T) {
	store := newStore(t)
	if _, err := store.Add(sampleInput("https://one.example", "Main")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := store.RenameSection(SectionRename{From: "Missing", To: "Other"}); !errors.Is(err, ErrSectionNotFound) {
		t.Fatalf("expected ErrSectionNotFound, got %v", err)
	}
	for name, in := range map[string]SectionRename{
		"empty from":      {From: "", To: "Other"},
		"empty to":        {From: "Main", To: ""},
		"whitespace only": {From: "Main", To: "   "},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := store.RenameSection(in); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestRenameSectionTakesBackup(t *testing.T) {
	store := newStore(t)
	if _, err := store.Add(sampleInput("https://one.example", "Main")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	backupDir := filepath.Join(filepath.Dir(store.Path()), backupDirName)
	before := countBackups(t, backupDir)

	if _, err := store.RenameSection(SectionRename{From: "Main", To: "Primary"}); err != nil {
		t.Fatalf("RenameSection: %v", err)
	}
	if got := countBackups(t, backupDir); got != before+1 {
		t.Errorf("backups = %d, want %d; a rename is a structural change", got, before+1)
	}
}

func TestRecordClickIncrements(t *testing.T) {
	store := newStore(t)
	link, _ := store.Add(sampleInput("https://example.com", "Main"))

	for i := 1; i <= 3; i++ {
		got, err := store.RecordClick(link.ID)
		if err != nil {
			t.Fatalf("RecordClick: %v", err)
		}
		if got.ClickCount != i {
			t.Errorf("click %d: count = %d, want %d", i, got.ClickCount, i)
		}
		if got.LastClicked == nil {
			t.Fatal("expected a last_clicked timestamp")
		}
	}
}

func TestRecordCheckStoresResult(t *testing.T) {
	store := newStore(t)
	link, _ := store.Add(sampleInput("https://example.com", "Main"))

	result := CheckResult{Status: 404, Result: "unknown", Detail: "404 Not Found"}
	if err := store.RecordCheck(map[string]CheckResult{link.ID: result}); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	got := readDoc(t, store.Path()).Sections[0].Links[0]
	if got.LastStatus != 404 || got.LastResult != "unknown" || got.LastDetail != "404 Not Found" {
		t.Errorf("got status=%d result=%q detail=%q", got.LastStatus, got.LastResult, got.LastDetail)
	}
	if got.LastChecked == nil {
		t.Error("expected a last_checked timestamp")
	}
}

func TestTargetsCarrySkipFlag(t *testing.T) {
	store := newStore(t)
	checked, _ := store.Add(sampleInput("https://checked.example", "Main"))
	skipped, err := store.Add(LinkInput{URL: "https://skipped.example", Section: "Main", SkipCheck: true})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	targets, err := store.Targets()
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	flags := map[string]bool{}
	for _, target := range targets {
		flags[target.ID] = target.Skip
	}
	if flags[checked.ID] {
		t.Error("ordinary link should not be skipped")
	}
	if !flags[skipped.ID] {
		t.Error("skip_check link should be skipped")
	}
}

func TestEnablingSkipClearsStaleHealth(t *testing.T) {
	store := newStore(t)
	link, _ := store.Add(sampleInput("https://example.com", "Main"))

	result := CheckResult{Status: 404, Result: "unknown", Detail: "404 Not Found"}
	if err := store.RecordCheck(map[string]CheckResult{link.ID: result}); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}

	in := sampleInput("https://example.com", "Main")
	in.SkipCheck = true
	updated, err := store.Update(link.ID, in)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.LastChecked != nil || updated.LastStatus != 0 || updated.LastResult != "" || updated.LastDetail != "" {
		t.Errorf("stale health survived the skip flag: %+v", updated)
	}
}

func TestBackupsOnlyOnStructuralChanges(t *testing.T) {
	store := newStore(t)
	backupDir := filepath.Join(filepath.Dir(store.Path()), backupDirName)

	link, _ := store.Add(sampleInput("https://example.com", "Main"))
	afterAdd := countBackups(t, backupDir)

	for i := 0; i < 5; i++ {
		if _, err := store.RecordClick(link.ID); err != nil {
			t.Fatalf("RecordClick: %v", err)
		}
	}
	if got := countBackups(t, backupDir); got != afterAdd {
		t.Errorf("clicks produced %d backups, want %d", got-afterAdd, 0)
	}

	if err := store.RecordCheck(map[string]CheckResult{link.ID: {Status: 200, Result: "alive"}}); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if got := countBackups(t, backupDir); got != afterAdd {
		t.Errorf("health check produced a backup; count = %d, want %d", got, afterAdd)
	}
}

func TestBackupsAreCappedAndDeduplicated(t *testing.T) {
	store := newStore(t)
	for i := 0; i < backupsKept+4; i++ {
		if _, err := store.Add(LinkInput{URL: urlFor(i), Section: "Main"}); err != nil {
			t.Fatalf("Add %d: %v", i, err)
		}
	}
	backupDir := filepath.Join(filepath.Dir(store.Path()), backupDirName)
	if got := countBackups(t, backupDir); got != backupsKept {
		t.Errorf("kept %d backups, want exactly %d", got, backupsKept)
	}

	// A structural write whose result is byte-identical to the newest
	// backup must not consume a slot.
	before := newestBackup(t, backupDir)
	link, err := store.Add(LinkInput{URL: "https://unique.example", Section: "Main"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.Delete(link.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := store.RecordCheck(map[string]CheckResult{}); err != nil {
		t.Fatalf("RecordCheck: %v", err)
	}
	if got := countBackups(t, backupDir); got != backupsKept {
		t.Errorf("kept %d backups after add+delete, want %d", got, backupsKept)
	}
	if after := newestBackup(t, backupDir); after == before {
		t.Error("expected the add+delete pair to rotate the backup set")
	}
}

// newestBackup returns the name of the most recent backup file.
func newestBackup(t *testing.T, dir string) string {
	t.Helper()
	names, err := backupFiles(dir)
	if err != nil {
		t.Fatalf("backupFiles: %v", err)
	}
	if len(names) == 0 {
		return ""
	}
	return names[len(names)-1]
}

func TestReloadsExternalEdits(t *testing.T) {
	store := newStore(t)
	if _, err := store.Add(sampleInput("https://example.com", "Main")); err != nil {
		t.Fatalf("Add: %v", err)
	}

	external := `{"title":"Edited","sections":[{"name":"Hand","links":[{"id":"abc","url":"https://hand.example","title":"Hand","notes":"","created":"2026-01-01T00:00:00Z","last_clicked":null,"click_count":0,"last_checked":null,"last_status":0}]}]}`
	if err := os.WriteFile(store.Path(), []byte(external), 0o644); err != nil {
		t.Fatalf("write external edit: %v", err)
	}

	data, err := store.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc.Title != "Edited" || len(doc.Sections) != 1 || doc.Sections[0].Name != "Hand" {
		t.Fatalf("external edit was not picked up: %+v", doc)
	}
}

func TestTargetsListsEveryLink(t *testing.T) {
	store := newStore(t)
	for i := 0; i < 3; i++ {
		if _, err := store.Add(LinkInput{URL: urlFor(i), Section: "Main"}); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	targets, err := store.Targets()
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 3 {
		t.Fatalf("got %d targets, want 3", len(targets))
	}
}

func TestSectionMatchIsCaseInsensitive(t *testing.T) {
	store := newStore(t)
	if _, err := store.Add(sampleInput("https://one.example", "Github")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := store.Add(sampleInput("https://two.example", "github")); err != nil {
		t.Fatalf("Add: %v", err)
	}
	doc := readDoc(t, store.Path())
	if len(doc.Sections) != 1 {
		t.Fatalf("expected one section, got %d", len(doc.Sections))
	}
}

// urlFor builds a distinct test URL for an index. The index is formatted as a
// number rather than a letter so that callers driven by backupsKept stay valid
// however large that constant grows.
func urlFor(i int) string {
	return fmt.Sprintf("https://example.com/page-%d", i)
}

// readDoc parses the store file directly, bypassing the in-memory copy.
func readDoc(t *testing.T, path string) Document {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return doc
}

// countBackups reports how many backup files currently exist.
func countBackups(t *testing.T, dir string) int {
	t.Helper()
	names, err := backupFiles(dir)
	if err != nil {
		t.Fatalf("backupFiles: %v", err)
	}
	return len(names)
}
