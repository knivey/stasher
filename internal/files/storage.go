// Package files stores uploaded photos and documents on disk under a root
// directory, sharded yyyy/mm. Storage keys never trust client input.
package files

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var keyRe = regexp.MustCompile(`^\d{4}/\d{2}/[A-Za-z0-9_-]{20,30}\.[A-Za-z0-9]{1,8}$`)

// ValidKey reports whether key is a well-formed storage key.
func ValidKey(key string) bool { return keyRe.MatchString(key) }

type Storage struct{ root string }

// New creates the root directory and returns a Storage.
func New(root string) (*Storage, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &Storage{root: root}, nil
}

// Save writes r to a fresh key with the given extension and returns the key
// and number of bytes written.
func (s *Storage) Save(r io.Reader, ext string) (string, int64, error) {
	key := s.newKey(ext)
	p := s.pathFor(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", 0, err
	}
	f, err := os.Create(p)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	n, err := io.Copy(f, r)
	if err != nil {
		os.Remove(p)
		return "", 0, err
	}
	return key, n, nil
}

// Open opens the file for key; invalid or missing keys return an error.
func (s *Storage) Open(key string) (*os.File, error) {
	if !ValidKey(key) {
		return nil, os.ErrNotExist
	}
	return os.Open(s.pathFor(key))
}

// Copy duplicates the file at key under a new key.
func (s *Storage) Copy(key string) (string, error) {
	f, err := s.Open(key)
	if err != nil {
		return "", err
	}
	defer f.Close()
	newKey, _, err := s.Save(f, strings.TrimPrefix(path.Ext(key), "."))
	return newKey, err
}

// Delete removes the file at key; missing files are not an error.
func (s *Storage) Delete(key string) error {
	if !ValidKey(key) {
		return nil
	}
	err := os.Remove(s.pathFor(key))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (s *Storage) newKey(ext string) string {
	ext = strings.TrimPrefix(ext, ".") // tolerate ".png" or "png"
	if ext == "" || len(ext) > 8 {
		ext = "bin"
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	now := time.Now().UTC()
	return fmt.Sprintf("%04d/%02d/%s.%s", now.Year(), int(now.Month()),
		base64.RawURLEncoding.EncodeToString(b), strings.ToLower(ext))
}

func (s *Storage) pathFor(key string) string {
	return filepath.Join(s.root, filepath.FromSlash(key))
}
