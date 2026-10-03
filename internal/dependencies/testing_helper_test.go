package dependencies

import (
	"archive/zip"
	"context"
	"os"
	"testing"
)

// buildZipFile writes files (name → content) into a real zip archive,
// mirroring how release archives appear to the extractor.
func buildZipFile(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, content := range files {
		fo, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if content != "" {
			if _, err := fo.Write([]byte(content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

var _ = context.Background
