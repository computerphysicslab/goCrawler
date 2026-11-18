package pdftextlib

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// buildTestPDF creates a minimal single-page PDF containing text.
func buildTestPDF(text string) []byte {
	var buf bytes.Buffer
	offsets := make([]int, 6)
	buf.WriteString("%PDF-1.4\n")

	offsets[1] = buf.Len()
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")
	offsets[2] = buf.Len()
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")
	offsets[3] = buf.Len()
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] " +
		"/Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>\nendobj\n")

	stream := "BT /F1 12 Tf 72 720 Td (" + text + ") Tj ET\n"
	offsets[4] = buf.Len()
	fmt.Fprintf(&buf, "4 0 obj\n<< /Length %d >>\nstream\n", len(stream))
	buf.WriteString(stream)
	buf.WriteString("endstream\nendobj\n")

	offsets[5] = buf.Len()
	buf.WriteString("5 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")

	xrefStart := buf.Len()
	buf.WriteString("xref\n0 6\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&buf, "%010d 00000 n \n", offsets[i])
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xrefStart)
	return buf.Bytes()
}

func TestExtractPlainText(t *testing.T) {
	data := buildTestPDF("Hello PDF world")
	text, pages, err := Extract(data, 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if !strings.Contains(text, "Hello PDF world") {
		t.Fatalf("text = %q", text)
	}
	if pages != 1 {
		t.Fatalf("pages = %d, want 1", pages)
	}
}

func TestExtractRejectsInvalidDocuments(t *testing.T) {
	if _, _, err := Extract(nil, 0); err == nil {
		t.Fatal("empty document accepted")
	}
	if _, _, err := Extract([]byte("not a pdf"), 0); err == nil {
		t.Fatal("non-PDF accepted")
	}
}
