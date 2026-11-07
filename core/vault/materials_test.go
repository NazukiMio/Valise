package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copyLeaf(t *testing.T, src string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "copy")
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(out, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func reader(dir string) func(string) ([]byte, error) {
	return func(rel string) ([]byte, error) { return os.ReadFile(joinRel(dir, rel)) }
}

func TestMaterialsCompareAndIntegrity(t *testing.T) {
	f := newFixture(t)
	f.seal(t, testData())
	f.seal(t, map[string][]byte{"small.md": []byte("changed\n")})
	os.WriteFile(filepath.Join(f.dir, DeletingFile), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(f.dir, PacksDir, "tmp-123"), []byte("x"), 0o600)
	basis, err := Materials(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range basis {
		if MaterialExcluded(m.Path) {
			t.Fatalf("excluded file listed: %s", m.Path)
		}
	}
	if len(Snapshots(basis)) != 2 {
		t.Fatalf("%v", Snapshots(basis))
	}
	if probs := IntegrityCheck(basis, reader(f.dir)); len(probs) != 0 {
		t.Fatal(probs)
	}

	c := copyLeaf(t, f.dir)
	have, _ := Materials(c)
	if d := CompareMaterials(basis, have); !d.OK() || MaterialsDigest(have) != MaterialsDigest(basis) {
		t.Fatalf("%+v", d)
	}

	// Same snapshot count, one pack missing: the old count rule passes it.
	var pack string
	for _, m := range basis {
		if IsPackPath(m.Path) {
			pack = m.Path
		}
	}
	os.Remove(joinRel(c, pack))
	os.Remove(joinRel(c, DedupeKeyPath(1)))
	have, _ = Materials(c)
	d := CompareMaterials(basis, have)
	if len(d.Missing) != 2 || len(Snapshots(have)) != 2 {
		t.Fatalf("%+v", d)
	}
	probs := IntegrityCheck(have, reader(c))
	joined := ""
	for _, p := range probs {
		joined += p.String() + "\n"
	}
	if !strings.Contains(joined, "不存在或未通过校验") || !strings.Contains(joined, "去重参数") {
		t.Fatal(joined)
	}
}

func TestCompareSmallFileAndPackHash(t *testing.T) {
	f := newFixture(t)
	f.seal(t, map[string][]byte{"a": []byte("hello")})
	basis, _ := Materials(f.dir)
	have := make([]Material, len(basis))
	copy(have, basis)
	for i := range have {
		switch {
		case IsPackPath(have[i].Path):
			have[i].SHA256 = "" // a listing without pack hashes still compares
		case strings.HasSuffix(have[i].Path, ".meta.json"):
			have[i].SHA256 = strings.Repeat("0", 64)
		case strings.HasPrefix(have[i].Path, EpochsDir):
			have[i].SHA256 = ""
		}
	}
	have = append(have, Material{Path: "packs/zz/extra.pack", Size: 1})
	d := CompareMaterials(basis, have)
	if len(d.Different) != 1 || len(d.Unhashed) != 1 || len(d.Extra) != 1 || len(d.Missing) != 0 {
		t.Fatalf("%+v", d)
	}
	// Without pack hashes the integrity check says the content is unchecked.
	probs := IntegrityCheck(have, reader(f.dir))
	found := false
	for _, p := range probs {
		found = found || p.What == "内容未核对"
	}
	if !found {
		t.Fatal(probs)
	}
}
