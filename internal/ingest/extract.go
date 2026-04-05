package ingest

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ExtractText extracts text content from a file based on its type.
func ExtractText(data []byte, filename, mimeType string) (string, error) {
	ext := strings.ToLower(filepath.Ext(filename))

	switch {
	case ext == ".md" || ext == ".txt" || ext == ".csv":
		return string(data), nil

	case ext == ".pdf" || mimeType == "application/pdf":
		return extractPDF(data)

	case ext == ".docx" || mimeType == "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return extractDocx(data)

	case ext == ".zip" || mimeType == "application/zip":
		return "", errors.New("zip files should be processed via ExtractZip")

	default:
		// Try as plain text
		return string(data), nil
	}
}

// extractPDF uses pdftotext (poppler-utils) to extract text from PDF data.
func extractPDF(data []byte) (string, error) {
	// Write to temp file
	tmp, err := os.CreateTemp("", "kit-pdf-*.pdf")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()

	if _, err := tmp.Write(data); err != nil {
		return "", fmt.Errorf("writing temp file: %w", err)
	}
	tmp.Close()

	// Run pdftotext
	cmd := exec.Command("pdftotext", "-layout", tmp.Name(), "-")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &bytes.Buffer{}

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("running pdftotext: %w", err)
	}

	return out.String(), nil
}

// extractDocx extracts text from a DOCX file by reading the XML content.
func extractDocx(data []byte) (string, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("opening docx: %w", err)
	}

	for _, f := range reader.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", fmt.Errorf("opening document.xml: %w", err)
			}
			defer rc.Close()

			content, err := io.ReadAll(rc)
			if err != nil {
				return "", fmt.Errorf("reading document.xml: %w", err)
			}

			// Simple XML text extraction — strip tags
			return stripXMLTags(string(content)), nil
		}
	}

	return "", errors.New("document.xml not found in docx")
}

// ExtractZip processes a zip file and returns a map of filename -> content.
func ExtractZip(data []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("opening zip: %w", err)
	}

	files := make(map[string][]byte)
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		files[f.Name] = content
	}
	return files, nil
}

func stripXMLTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			result.WriteRune(' ')
		case !inTag:
			result.WriteRune(r)
		}
	}
	// Clean up whitespace
	text := result.String()
	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.Join(cleaned, "\n")
}
