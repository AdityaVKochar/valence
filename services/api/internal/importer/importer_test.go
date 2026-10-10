package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/AdityaVKochar/valence/gen/go/db"
	"github.com/AdityaVKochar/valence/pkg/blob"
	"github.com/AdityaVKochar/valence/pkg/blob/memblob"
	"github.com/AdityaVKochar/valence/pkg/pg/pgtest"
	"github.com/AdityaVKochar/valence/pkg/problempkg"
)

func TestImportIsIdempotent(t *testing.T) {
	pool := pgtest.New(t)
	blobs := memblob.New()
	im := New(pool, blobs)
	ctx := context.Background()

	src, err := problempkg.Load("../../../../problems/examples/aplusb")
	if err != nil {
		t.Fatal(err)
	}
	first, err := im.Import(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if first.Result != Created || first.Revision != 1 || first.Tests != len(src.Tests) {
		t.Fatalf("first import %+v", first)
	}
	second, err := im.Import(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if second.Result != Unchanged || second.Revision != 1 {
		t.Fatalf("second import %+v, want unchanged at revision 1", second)
	}

	dir := copyPackage(t, "../../../../problems/examples/aplusb")
	if err := os.WriteFile(filepath.Join(dir, "tests", "01.out"), []byte("4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := problempkg.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	third, err := im.Import(ctx, changed)
	if err != nil {
		t.Fatal(err)
	}
	if third.Result != Updated || third.Revision != 2 {
		t.Fatalf("third import %+v, want updated at revision 2", third)
	}
	q := db.New(pool)
	p, _ := q.GetProblemBySlug(ctx, "aplusb")
	tests, _ := q.ListTests(ctx, p.ID)
	if len(tests) != len(src.Tests) || !tests[0].IsSample {
		t.Fatalf("tests after re-import: %+v", tests)
	}
	sum := sha256.Sum256([]byte("4\n"))
	if ok, _ := blobs.Exists(ctx, blob.Key(hex.EncodeToString(sum[:]))); !ok {
		t.Fatal("new output was not stored")
	}
}

func copyPackage(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}
