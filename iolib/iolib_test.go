package iolib

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if FileExists(path) {
		t.Fatal("missing file reported as existing")
	}
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if !FileExists(path) {
		t.Fatal("existing file reported as missing")
	}
}

func TestString2fileAndAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.txt")
	String2file("first", path)
	String2fileAppend("second", path)
	got := File2string(path)
	if got != "firstsecond\n" {
		t.Fatalf("File2string = %q, want %q", got, "firstsecond\n")
	}
}

func TestCopyFileContents(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("payload"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CopyFileContents(src, dst); err != nil {
		t.Fatal(err)
	}
	if got := File2string(dst); got != "payload" {
		t.Fatalf("CopyFileContents = %q, want payload", got)
	}
}

func TestCopyFileContentsMissingSource(t *testing.T) {
	dir := t.TempDir()
	if err := CopyFileContents(filepath.Join(dir, "none"), filepath.Join(dir, "out")); err == nil {
		t.Fatal("copying a missing file should fail")
	}
}
