package checkpoint

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMakeLeavesUserStateAlone(t *testing.T) {
	d := t.TempDir()
	g := func(a ...string) string {
		o, err := git(d, []string{"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t"}, a...)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	g("init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(d, "a.txt"), []byte("1"), 0o644)
	g("add", "-A")
	g("commit", "-qm", "init")
	headBefore := g("rev-parse", "HEAD")

	os.WriteFile(filepath.Join(d, "a.txt"), []byte("2"), 0o644)   // modified
	os.WriteFile(filepath.Join(d, "new.txt"), []byte("n"), 0o644) // untracked
	br, c1, err := Make(d, "r1", "wip")
	if err != nil {
		t.Fatal(err)
	}
	if g("rev-parse", "HEAD") != headBefore || g("rev-parse", "--abbrev-ref", "HEAD") != "main" {
		t.Fatal("user branch moved")
	}
	if g("show", br+":new.txt") != "n" || g("show", br+":a.txt") != "2" {
		t.Fatal("wip branch misses work")
	}
	if st := g("status", "--porcelain"); st == "" {
		t.Fatal("working tree was cleaned")
	}
	if _, c2, _ := Make(d, "r1", "wip"); c2 != c1 {
		t.Fatal("unchanged tree must not add a commit")
	}
	os.WriteFile(filepath.Join(d, "a.txt"), []byte("3"), 0o644)
	if _, c3, _ := Make(d, "r1", "wip"); c3 == c1 || g("rev-parse", c3+"^") != c1 {
		t.Fatal("changed tree must chain a new commit")
	}
}
