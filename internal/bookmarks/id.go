package bookmarks

import (
	"crypto/rand"
	"encoding/hex"
)

// idBytes is the number of random bytes behind each link ID, yielding a
// 12-character hex string.
const idBytes = 6

// newID returns a random hex identifier for a link. It panics only if the
// system entropy source fails, which is not a recoverable condition.
func newID() string {
	buf := make([]byte, idBytes)
	if _, err := rand.Read(buf); err != nil {
		panic("bookmarks: entropy source failed: " + err.Error())
	}
	return hex.EncodeToString(buf)
}
