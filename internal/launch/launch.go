// Package launch hands a URL to the operating system, which opens it with
// whatever application is registered for it: the default browser for an http
// URL, and the file's own application for a file URL. The server uses it to
// open its own page at startup and to open file links, which browsers refuse
// to follow from an http page.
package launch

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Open asks the platform to open the given URL with its registered handler.
// It returns once the launcher has started, without waiting for it to finish.
func Open(url string) error {
	var name string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		name = "xdg-open"
	}

	cmd := exec.Command(name, append(args, url)...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch %s: %w", name, err)
	}
	// Reap the launcher when it exits so it does not linger as a zombie for
	// the life of the server. Its exit status is of no interest.
	go cmd.Wait()
	return nil
}
