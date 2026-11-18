package corpusfreqlib

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a")
	if fileExists(path) {
		t.Fatal("missing file reported as existing")
	}
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if !fileExists(path) {
		t.Fatal("existing file reported as missing")
	}
	if fileExists(dir) {
		t.Fatal("directory should not be reported as a file")
	}
}

func TestLoadCorpus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "all.num")
	content := "100 the at0 1\n50 image nn1 2\n20 stack nn1 3\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := loadCorpus(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("loadCorpus returned %d entries, want 3", len(got))
	}
	if got["image"].numTotal != 50 || got["image"].POStagging != "nn1" {
		t.Fatalf("unexpected WordInfo: %+v", got["image"])
	}
}

func TestInitAndFreq(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(".."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)

	if err := Init(); err != nil {
		t.Fatal(err)
	}
	if Freq("the") <= 0 {
		t.Fatal("Freq(the) should be positive after Init")
	}
	if Freq("definitely-not-a-real-token-xyz") != 0 {
		t.Fatal("unknown token should have zero frequency")
	}
}

func TestGenerateIndexFromSmallFixture(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "freq-index.json.gz")
	err := GenerateIndex(
		filepath.Join("testdata", "all.num.small"),
		filepath.Join("testdata", "contemporaryEnglish.small.txt"),
		output,
	)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	index := make(map[string]int)
	if err := json.Unmarshal(b, &index); err != nil {
		t.Fatal(err)
	}
	if index["the"] <= 0 || index["image"] <= 0 {
		t.Fatalf("unexpected generated index: %v", index)
	}
	if _, ok := index["noise"]; !ok {
		t.Fatal("generated index lost a BNC-only token")
	}
}
