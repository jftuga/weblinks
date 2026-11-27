// This file owns the rolling backups taken before every structural change,
// and the restore that puts one back. Backups are plain copies of the store
// file under timestamped names, which keeps them readable and recoverable with
// nothing but a text editor if this program is unavailable. A restore is
// itself a structural change, so the document it replaces is backed up first
// and restoring the wrong one is undoable.

package bookmarks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// backupsKept is the number of rolling backups retained on disk.
const backupsKept = 30

// backupDirName is the directory, relative to the JSON file, holding backups.
const backupDirName = "backups"

// backupPrefix and backupExt bracket the timestamp in a backup file name.
const (
	backupPrefix = "weblinks-"
	backupExt    = ".json"
)

// backupStamp names backup files. It carries nanoseconds so that several
// structural changes in quick succession cannot overwrite one another, and
// is fixed width so that sorting the names lexically also sorts them
// chronologically.
const backupStamp = "20060102T150405.000000000Z"

// BackupInfo describes one rolling backup. Sections and Links are counted from
// the file's contents so that a restore can be chosen by what it holds rather
// than by timestamp alone.
type BackupInfo struct {
	Name     string    `json:"name"`
	Taken    time.Time `json:"taken"`
	Size     int64     `json:"size"`
	Sections int       `json:"sections"`
	Links    int       `json:"links"`
}

// Backups lists the retained backups, newest first. A file that no longer
// parses is reported with zero counts rather than failing the whole listing,
// since the others are still restorable.
func (s *Store) Backups() ([]BackupInfo, error) {
	// The path is fixed at Open and the backup files are only ever created or
	// pruned, never rewritten, so listing them needs no lock.
	dir := s.backupDir()
	names, err := backupFiles(dir)
	if err != nil {
		return nil, err
	}

	infos := make([]BackupInfo, 0, len(names))
	for i := len(names) - 1; i >= 0; i-- {
		info, err := describeBackup(dir, names[i])
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// Restore replaces the live document with the contents of a backup. The
// current document is backed up before being overwritten. It returns
// ErrBackupNotFound for a name that does not exist and refuses a name that is
// not a plain backup file name, so a request cannot reach outside the backup
// directory.
func (s *Store) Restore(name string) error {
	if err := validBackupName(name); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(filepath.Join(s.backupDir(), name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrBackupNotFound, name)
		}
		return fmt.Errorf("read backup %q: %w", name, err)
	}

	doc := &Document{}
	if err := json.Unmarshal(data, doc); err != nil {
		return fmt.Errorf("parse backup %q: %w", name, err)
	}
	for _, section := range doc.Sections {
		for _, link := range section.Links {
			if link.ID == "" {
				link.ID = newID()
			}
		}
	}

	// The backup taken inside persist copies what is still on disk, which is
	// the document being replaced, so this restore can itself be undone.
	s.doc = doc
	return s.persist(true)
}

// backupDir returns the directory holding this store's backups.
func (s *Store) backupDir() string {
	return filepath.Join(filepath.Dir(s.path), backupDirName)
}

// backup copies the current file into the backups directory under a
// timestamped name, then prunes the oldest copies beyond backupsKept. A
// backup identical to the most recent one is skipped.
func (s *Store) backup() error {
	current, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %q for backup: %w", s.path, err)
	}

	dir := s.backupDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create backup directory %q: %w", dir, err)
	}

	existing, err := backupFiles(dir)
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		newest := filepath.Join(dir, existing[len(existing)-1])
		if prev, err := os.ReadFile(newest); err == nil && bytes.Equal(prev, current) {
			return nil
		}
	}

	name := fmt.Sprintf("%s%s%s", backupPrefix, time.Now().UTC().Format(backupStamp), backupExt)
	if err := os.WriteFile(filepath.Join(dir, name), current, 0o644); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}

	existing = append(existing, name)
	for i := 0; i < len(existing)-backupsKept; i++ {
		os.Remove(filepath.Join(dir, existing[i]))
	}
	return nil
}

// backupFiles lists backup file names in the directory, oldest first. The
// timestamped naming scheme sorts lexically in chronological order.
func backupFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backup directory %q: %w", dir, err)
	}
	var names []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && filepath.Ext(name) == backupExt {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

// describeBackup reads one backup and summarizes what restoring it would
// bring back.
func describeBackup(dir, name string) (BackupInfo, error) {
	path := filepath.Join(dir, name)
	stat, err := os.Stat(path)
	if err != nil {
		return BackupInfo{}, fmt.Errorf("stat backup %q: %w", name, err)
	}

	info := BackupInfo{Name: name, Size: stat.Size(), Taken: backupTime(name, stat.ModTime())}

	data, err := os.ReadFile(path)
	if err != nil {
		return BackupInfo{}, fmt.Errorf("read backup %q: %w", name, err)
	}
	doc := &Document{}
	if err := json.Unmarshal(data, doc); err != nil {
		// A corrupt backup is still worth listing; restoring it will fail
		// with a clear error, which is better than hiding the file.
		return info, nil
	}
	info.Sections = len(doc.Sections)
	for _, section := range doc.Sections {
		info.Links += len(section.Links)
	}
	return info, nil
}

// backupTime recovers the moment a backup was taken from its file name,
// falling back to the file's modification time when the name predates the
// current scheme.
func backupTime(name string, fallback time.Time) time.Time {
	stamp := strings.TrimSuffix(strings.TrimPrefix(name, backupPrefix), backupExt)
	taken, err := time.Parse(backupStamp, stamp)
	if err != nil {
		return fallback.UTC()
	}
	return taken.UTC()
}

// validBackupName rejects anything that is not a bare backup file name. The
// name arrives from an HTTP request, so a path separator or a parent
// reference must never reach the filesystem.
func validBackupName(name string) error {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid backup name %q", name)
	}
	if !strings.HasPrefix(name, backupPrefix) || !strings.HasSuffix(name, backupExt) {
		return fmt.Errorf("invalid backup name %q", name)
	}
	return nil
}
