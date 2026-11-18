// Package pdftextlib extracts plain text from PDF documents.
package pdftextlib

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/ledongthuc/pdf"
)

const (
	// DefaultPageLimit bounds extraction when the caller does not provide a limit.
	DefaultPageLimit = 200
	maxOutputRunes   = 4_000_000
)

// ErrNoText reports a PDF without an extractable text layer.
var ErrNoText = errors.New("pdf: no extractable text")

// Extract returns the plain text of data, limited to pageLimit pages when positive.
func Extract(data []byte, pageLimit int) (string, int, error) {
	if len(data) == 0 {
		return "", 0, errors.New("pdf: empty document")
	}
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		return "", 0, errors.New("pdf: missing %PDF header")
	}
	if pageLimit <= 0 {
		pageLimit = DefaultPageLimit
	}

	reader, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", 0, fmt.Errorf("pdf: open: %w", err)
	}

	pages := reader.NumPage()
	if pages <= 0 {
		return "", 0, ErrNoText
	}
	if pages > pageLimit {
		pages = pageLimit
	}

	var out strings.Builder
	seenPages := 0
	for i := 1; i <= pages; i++ {
		p := reader.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(text)
		seenPages++
		if out.Len() > maxOutputRunes {
			break
		}
	}

	plain := strings.TrimSpace(out.String())
	if plain == "" {
		return "", seenPages, ErrNoText
	}
	return plain, seenPages, nil
}
