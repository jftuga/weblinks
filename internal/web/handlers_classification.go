package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

const classificationSectionsPlaceholder = "{{sections}}"

// loadClassificationPrompt reads and validates the external prompt. Startup and
// request handling share this check, and each call reads the current file.
func loadClassificationPrompt(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read classification file; check -classification-file: %w", err)
	}
	source := string(data)
	if !strings.Contains(source, classificationSectionsPlaceholder) {
		return "", fmt.Errorf("classification file %q must contain {{sections}} for the current section names", path)
	}
	return source, nil
}

// handleClassification reads the external prompt on every request and inserts
// the current section names. Neither the file nor the rendered response is
// cached, including failures, so fixing the file takes effect on the next open.
func (s *Server) handleClassification(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	source, err := loadClassificationPrompt(s.classificationFile)
	if err != nil {
		serverError(w, "load classification prompt", err)
		return
	}
	names, err := s.store.SectionNames()
	if err != nil {
		serverError(w, "read classification sections", err)
		return
	}
	// JSON preserves quotes, line breaks and other special characters in names
	// without letting them introduce additional Markdown headings or list items.
	// This is plain text, not HTML. Keep &, < and > readable in the prompt;
	// the browser escapes the text separately when displaying it as markup.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(names); err != nil {
		serverError(w, "encode classification sections", err)
		return
	}
	sections := bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
	prompt := strings.ReplaceAll(source, classificationSectionsPlaceholder, string(sections))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if _, err := w.Write([]byte(prompt)); err != nil {
		log.Printf("write classification response: %v", err)
	}
}
