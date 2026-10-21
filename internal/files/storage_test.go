package files

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStorage(t *testing.T) *Storage {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "uploads"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSaveOpenRoundtrip(t *testing.T) {
	s := newStorage(t)
	key, size, err := s.Save(strings.NewReader("hello world"), "txt")
	if err != nil {
		t.Fatal(err)
	}
	if size != 11 || !ValidKey(key) {
		t.Fatalf("key=%q size=%d", key, size)
	}
	f, err := s.Open(key)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, _ := io.ReadAll(f)
	if string(b) != "hello world" {
		t.Fatalf("read back %q", b)
	}
	// file must live under root
	abs, _ := filepath.Abs(f.Name())
	rootAbs, _ := filepath.Abs(s.root)
	if !strings.HasPrefix(abs, rootAbs+string(os.PathSeparator)) {
		t.Fatalf("file %s escaped root %s", abs, rootAbs)
	}
}

func TestSavePreservesExtension(t *testing.T) {
	s := newStorage(t)
	key, _, err := s.Save(strings.NewReader("x"), "txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(key, ".txt") {
		t.Fatalf("dotless ext: key=%q, want .txt suffix", key)
	}
	key, _, err = s.Save(strings.NewReader("x"), ".png")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(key, ".png") {
		t.Fatalf("dotted ext: key=%q, want .png suffix", key)
	}
}

func TestOpenRejectsBadKeys(t *testing.T) {
	s := newStorage(t)
	for _, key := range []string{
		"../../etc/passwd", "../secrets.txt", "/abs/path.png",
		"2026/10/ok.png/../../../x", "noext", "2026/10/x.sh.exe.png.exe.exe",
		"", "2026/10/.png",
	} {
		if ValidKey(key) {
			t.Errorf("ValidKey(%q) = true", key)
		}
		if _, err := s.Open(key); err == nil {
			t.Errorf("Open(%q) succeeded, want error", key)
		}
	}
}

func TestCopyDuplicatesContent(t *testing.T) {
	s := newStorage(t)
	key, _, _ := s.Save(strings.NewReader("doc-bytes"), "pdf")
	key2, err := s.Copy(key)
	if err != nil {
		t.Fatal(err)
	}
	if key2 == key {
		t.Fatal("copy returned same key")
	}
	if !strings.HasSuffix(key, ".pdf") || !strings.HasSuffix(key2, ".pdf") {
		t.Fatalf("extension not preserved: key=%q key2=%q", key, key2)
	}
	f, _ := s.Open(key2)
	defer f.Close()
	b, _ := io.ReadAll(f)
	if string(b) != "doc-bytes" {
		t.Fatalf("copy content %q", b)
	}
	s.Delete(key)
	f2, err := s.Open(key2)
	if err != nil {
		t.Fatal("copy died with original")
	}
	f2.Close()
}

func TestDeleteIdempotent(t *testing.T) {
	s := newStorage(t)
	key, _, _ := s.Save(strings.NewReader("x"), "png")
	if err := s.Delete(key); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(key); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if err := s.Delete("2026/10/nothere.png"); err != nil {
		t.Fatalf("delete of invalid-key missing file: %v", err)
	}
}
