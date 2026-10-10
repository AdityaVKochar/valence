package localblob

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AdityaVKochar/valence/pkg/blob"
	"github.com/AdityaVKochar/valence/pkg/blob/blobtest"
)

func TestConformance(t *testing.T) {
	blobtest.Run(t, func(t *testing.T) blob.Store {
		s, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return s
	})
}

func TestShardedLayout(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	key, _, err := s.Put(t.Context(), strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	k := string(key)
	if _, err := os.Stat(filepath.Join(dir, k[:2], k[2:4], k)); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "tmp"))
	if len(entries) != 0 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}
