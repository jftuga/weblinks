// This file exposes the rolling backups the store keeps. Without it the
// backups are only reachable by copying a file over the live one by hand,
// which makes an accidental delete permanent in practice. Listing reports what
// each backup holds so one can be chosen by contents rather than timestamp.

package web

import "net/http"

// handleListBackups returns the retained backups, newest first.
func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	backups, err := s.store.Backups()
	if err != nil {
		serverError(w, "list backups", err)
		return
	}
	writeJSON(w, http.StatusOK, backups)
}

// handleRestoreBackup replaces every bookmark with the contents of one backup.
// The document being replaced is backed up first, so restoring the wrong one
// is recoverable.
func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.store.Restore(name); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"restored": name})
}
