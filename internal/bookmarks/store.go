package bookmarks

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Sentinel errors returned by store mutations.
var (
	// ErrNotFound reports that no link exists with the requested ID.
	ErrNotFound = errors.New("link not found")
	// ErrDuplicateURL reports that another link already uses the given URL.
	ErrDuplicateURL = errors.New("url already saved as a weblink")
	// ErrSectionNotFound reports that no section carries the requested name.
	ErrSectionNotFound = errors.New("section not found")
	// ErrDuplicateSection reports that another section already uses the given
	// name.
	ErrDuplicateSection = errors.New("section already exists")
	// ErrBackupNotFound reports that no backup carries the requested name.
	ErrBackupNotFound = errors.New("backup not found")
)

// defaultTitle is used when a new store file has to be created from scratch.
const defaultTitle = "Weblinks"

// Store owns the JSON file and serializes all access to the document it
// holds. Reads reload the file first when it has changed on disk, so
// hand-edits are picked up without a restart. Writes are atomic: the document
// is marshaled to a temporary file in the same directory and renamed over the
// original, so an interrupted write can never truncate the store.
type Store struct {
	mu      sync.RWMutex
	path    string
	doc     *Document
	modTime time.Time
	size    int64
}

// Open loads the store at path, creating an empty document if the file does
// not yet exist.
func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", path, err)
	}
	s := &Store{path: abs}
	if err := s.load(); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		s.doc = &Document{Title: defaultTitle}
		if err := s.persist(false); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Path returns the absolute path of the backing JSON file.
func (s *Store) Path() string {
	return s.path
}

// load reads and parses the file, recording its modification stamp.
func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	doc := &Document{}
	if err := json.Unmarshal(data, doc); err != nil {
		return fmt.Errorf("parse %q: %w", s.path, err)
	}
	for _, section := range doc.Sections {
		for _, link := range section.Links {
			if link.ID == "" {
				link.ID = newID()
			}
		}
	}
	s.doc = doc
	if info, err := os.Stat(s.path); err == nil {
		s.modTime = info.ModTime()
		s.size = info.Size()
	}
	return nil
}

// reloadIfChanged re-reads the file when its size or modification time no
// longer matches what was recorded at the last read or write. A failed reload
// is reported but leaves the in-memory document intact.
func (s *Store) reloadIfChanged() error {
	info, err := os.Stat(s.path)
	if err != nil {
		return fmt.Errorf("stat %q: %w", s.path, err)
	}
	if info.ModTime().Equal(s.modTime) && info.Size() == s.size {
		return nil
	}
	return s.load()
}

// persist writes the in-memory document back to disk atomically. When
// structural is true a rolling backup of the previous contents is taken
// first; click and health-check updates pass false so that routine writes do
// not churn through the backup history.
func (s *Store) persist(structural bool) error {
	data, err := json.MarshalIndent(s.doc, "", "  ")
	if err != nil {
		return fmt.Errorf("encode store: %w", err)
	}
	data = append(data, '\n')

	if structural {
		if err := s.backup(); err != nil {
			return err
		}
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".weblinks-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace %q: %w", s.path, err)
	}

	if info, err := os.Stat(s.path); err == nil {
		s.modTime = info.ModTime()
		s.size = info.Size()
	}
	return nil
}

// JSON returns the document encoded for the API, reloading first if the file
// changed on disk.
func (s *Store) JSON() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadIfChanged(); err != nil {
		return nil, err
	}
	return json.Marshal(s.doc)
}

// SectionNames returns a copy of the current section names in document order,
// reloading first if the store changed on disk.
func (s *Store) SectionNames() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadIfChanged(); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(s.doc.Sections))
	for _, section := range s.doc.Sections {
		names = append(names, section.Name)
	}
	return names, nil
}

// Target is the minimal view of a link needed to check whether it still
// resolves.
type Target struct {
	ID   string
	URL  string
	Skip bool
}

// Targets returns every link for the dead-link checker, flagging the ones the
// user has excluded from checking.
func (s *Store) Targets() ([]Target, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadIfChanged(); err != nil {
		return nil, err
	}
	var targets []Target
	for _, section := range s.doc.Sections {
		for _, link := range section.Links {
			targets = append(targets, Target{ID: link.ID, URL: link.URL, Skip: link.SkipCheck})
		}
	}
	return targets, nil
}

// Get returns the link with the given ID, or ErrNotFound when there is none.
func (s *Store) Get(id string) (Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadIfChanged(); err != nil {
		return Link{}, err
	}
	link, _ := s.doc.findLink(id)
	if link == nil {
		return Link{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return *link, nil
}

// Lookup returns the link whose URL matches rawURL once both are normalized,
// or ErrNotFound when the URL is not bookmarked. It lets the client apply the
// same duplicate rule the store enforces instead of its own approximation.
func (s *Store) Lookup(rawURL string) (Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadIfChanged(); err != nil {
		return Link{}, err
	}
	link := s.doc.findByURL(rawURL, "")
	if link == nil {
		return Link{}, fmt.Errorf("%w: %s", ErrNotFound, rawURL)
	}
	return *link, nil
}

// Add creates a link in the named section, creating the section when it does
// not already exist. It returns ErrDuplicateURL when the URL is already
// bookmarked anywhere in the store.
func (s *Store) Add(in LinkInput) (Link, error) {
	if err := in.normalize(); err != nil {
		return Link{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadIfChanged(); err != nil {
		return Link{}, err
	}
	if dup := s.doc.findByURL(in.URL, ""); dup != nil {
		return Link{}, fmt.Errorf("%w: %q", ErrDuplicateURL, dup.Title)
	}

	section := s.doc.findSection(in.Section)
	if section == nil {
		section = &Section{Name: in.Section}
		s.doc.Sections = append(s.doc.Sections, section)
	}
	link := &Link{
		ID:        newID(),
		URL:       in.URL,
		Title:     in.Title,
		Notes:     in.Notes,
		Created:   time.Now().UTC(),
		SkipCheck: in.SkipCheck,
	}
	section.Links = append(section.Links, link)

	if err := s.persist(true); err != nil {
		return Link{}, err
	}
	return *link, nil
}

// Update replaces the editable fields of a link, moving it between sections
// when the section name changes. Sections left empty by a move are removed.
func (s *Store) Update(id string, in LinkInput) (Link, error) {
	if err := in.normalize(); err != nil {
		return Link{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadIfChanged(); err != nil {
		return Link{}, err
	}
	link, owner := s.doc.findLink(id)
	if link == nil {
		return Link{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if dup := s.doc.findByURL(in.URL, id); dup != nil {
		return Link{}, fmt.Errorf("%w: %q", ErrDuplicateURL, dup.Title)
	}

	link.URL = in.URL
	link.Title = in.Title
	link.Notes = in.Notes
	link.SkipCheck = in.SkipCheck
	if in.SkipCheck {
		// Stale health would otherwise keep flagging a link the user has
		// just declared unverifiable.
		link.clearHealth()
	}

	if !strings.EqualFold(owner.Name, in.Section) {
		target := s.doc.findSection(in.Section)
		if target == nil {
			target = &Section{Name: in.Section}
			s.doc.Sections = append(s.doc.Sections, target)
		}
		owner.removeLink(id)
		target.Links = append(target.Links, link)
		s.pruneEmptySections()
	}

	if err := s.persist(true); err != nil {
		return Link{}, err
	}
	return *link, nil
}

// RenameSection changes a section's name in place, leaving its links and its
// position in the document untouched. A name already held by a different
// section is refused rather than merging the two, but a change of letter case
// alone is allowed because it names the same section. It returns the stored
// name on success.
func (s *Store) RenameSection(in SectionRename) (string, error) {
	if err := in.normalize(); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadIfChanged(); err != nil {
		return "", err
	}
	section := s.doc.findSection(in.From)
	if section == nil {
		return "", fmt.Errorf("%w: %q", ErrSectionNotFound, in.From)
	}
	if !strings.EqualFold(section.Name, in.To) {
		if clash := s.doc.findSection(in.To); clash != nil {
			return "", fmt.Errorf("%w: %q", ErrDuplicateSection, clash.Name)
		}
	}

	section.Name = in.To
	if err := s.persist(true); err != nil {
		return "", err
	}
	return section.Name, nil
}

// SetTitle replaces the document title shown in the page heading and the
// browser tab. Surrounding whitespace is trimmed and a blank title is refused.
// It returns the stored title on success.
func (s *Store) SetTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("title is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadIfChanged(); err != nil {
		return "", err
	}

	s.doc.Title = title
	if err := s.persist(true); err != nil {
		return "", err
	}
	return s.doc.Title, nil
}

// Delete removes a link, pruning its section if that leaves it empty.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadIfChanged(); err != nil {
		return err
	}
	link, owner := s.doc.findLink(id)
	if link == nil {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	owner.removeLink(id)
	s.pruneEmptySections()
	return s.persist(true)
}

// RecordClick stamps a link as just clicked and increments its counter. This
// is a routine write and does not produce a backup.
func (s *Store) RecordClick(id string) (Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadIfChanged(); err != nil {
		return Link{}, err
	}
	link, _ := s.doc.findLink(id)
	if link == nil {
		return Link{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	now := time.Now().UTC()
	link.LastClicked = &now
	link.ClickCount++
	if err := s.persist(false); err != nil {
		return Link{}, err
	}
	return *link, nil
}

// RecordCheck stores dead-link results keyed by link ID. Links absent from
// the map are left untouched. This is a routine write and does not produce a
// backup.
func (s *Store) RecordCheck(results map[string]CheckResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.reloadIfChanged(); err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, section := range s.doc.Sections {
		for _, link := range section.Links {
			result, ok := results[link.ID]
			if !ok {
				continue
			}
			checked := now
			link.LastChecked = &checked
			link.LastStatus = result.Status
			link.LastResult = result.Result
			link.LastDetail = result.Detail
		}
	}
	return s.persist(false)
}

// pruneEmptySections drops sections that no longer contain any links.
// slices.DeleteFunc clears the vacated tail of the backing array, so dropped
// sections are not kept reachable from there.
func (s *Store) pruneEmptySections() {
	s.doc.Sections = slices.DeleteFunc(s.doc.Sections, func(section *Section) bool {
		return len(section.Links) == 0
	})
}
