package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseStatus(t *testing.T) {
	// Sortie de git status --porcelain=v2 --branch -z (séparateurs \0).
	out := strings.Join([]string{
		"# branch.oid 9a20003e656c22274c179f6c00e5055476b03d0d",
		"# branch.head main",
		"# branch.upstream origin/main",
		"# branch.ab +4 -1",
		"1 .M N... 100644 100644 100644 7f3a 7f3a README.md",
		"1 A. N... 000000 100644 100644 0000 1111 internal/neuf.go",
		"2 R. N... 100644 100644 100644 2222 2222 R100 docs/nouveau nom.md",
		"docs/ancien.md",
		"u UU N... 100644 100644 100644 100644 a b c conflit.go",
		"? notes avec espaces.txt",
		"",
	}, "\x00")
	s, err := ParseStatus([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	if s.Branch != "main" || s.Commit != "9a20003" || s.Upstream != "origin/main" || s.Ahead != 4 || s.Behind != 1 {
		t.Errorf("branche : %+v", s)
	}
	if len(s.Changes) != 5 {
		t.Fatalf("%d changements : %+v", len(s.Changes), s.Changes)
	}
	readme, neuf, ren, conflict, untracked := s.Changes[0], s.Changes[1], s.Changes[2], s.Changes[3], s.Changes[4]
	if readme.IsStaged() || !readme.IsUnstaged() || readme.Worktree != 'M' {
		t.Errorf("README modifié non indexé : %+v", readme)
	}
	if !neuf.IsStaged() || neuf.IsUnstaged() || neuf.Staged != 'A' {
		t.Errorf("fichier ajouté : %+v", neuf)
	}
	if ren.Path != "docs/nouveau nom.md" || ren.OrigPath != "docs/ancien.md" || ren.Staged != 'R' {
		t.Errorf("renommage : %+v", ren)
	}
	if !conflict.Conflict || conflict.IsStaged() || conflict.Path != "conflit.go" {
		t.Errorf("conflit : %+v", conflict)
	}
	if !untracked.Untracked() || untracked.Path != "notes avec espaces.txt" {
		t.Errorf("non suivi : %+v", untracked)
	}

	empty, _ := ParseStatus([]byte("# branch.oid (initial)\x00# branch.head main\x00"))
	if empty.Commit != "" || empty.Branch != "main" {
		t.Errorf("dépôt sans commit : %+v", empty)
	}
}

// repo crée un dépôt git temporaire.
func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.com"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v : %v %s", args, err, out)
		}
	}
	return dir
}

func TestWorkflow(t *testing.T) {
	ctx := context.Background()
	dir := repo(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("un\n"), 0o644)

	s, err := GetStatus(ctx, dir)
	if err != nil || len(s.Changes) != 1 || !s.Changes[0].Untracked() || s.Commit != "" {
		t.Fatalf("avant le premier commit : %+v, err=%v", s, err)
	}
	if d, err := Diff(ctx, dir, s.Changes[0], false); err != nil || !strings.Contains(d, "+un") {
		t.Errorf("diff d'un fichier non suivi : %q, err=%v", d, err)
	}
	if commits, err := Log(ctx, dir, 5); err != nil || len(commits) != 0 {
		t.Errorf("historique vide attendu : %v, err=%v", commits, err)
	}

	// Indexer puis désindexer sans commit.
	if err := Stage(ctx, dir, "a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := Unstage(ctx, dir, false, "a.txt"); err != nil {
		t.Fatal(err)
	}
	if s, _ := GetStatus(ctx, dir); !s.Changes[0].Untracked() {
		t.Errorf("après désindexation : %+v", s.Changes)
	}

	// Premier commit.
	StageAll(ctx, dir)
	hash, err := CommitStaged(ctx, dir, "Premier commit")
	if err != nil || len(hash) < 7 {
		t.Fatalf("commit : %q, err=%v", hash, err)
	}
	if _, err := CommitStaged(ctx, dir, "  "); err == nil {
		t.Error("un message vide doit être refusé")
	}

	// Modification indexée, puis désindexée avec un commit existant.
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("un\ndeux\n"), 0o644)
	Stage(ctx, dir, "a.txt")
	s, _ = GetStatus(ctx, dir)
	if !s.Changes[0].IsStaged() {
		t.Fatalf("modification indexée attendue : %+v", s.Changes)
	}
	if d, _ := Diff(ctx, dir, s.Changes[0], true); !strings.Contains(d, "+deux") {
		t.Errorf("diff indexé : %q", d)
	}
	if err := Unstage(ctx, dir, true, "a.txt"); err != nil {
		t.Fatal(err)
	}
	s, _ = GetStatus(ctx, dir)
	if s.Changes[0].IsStaged() || !s.Changes[0].IsUnstaged() {
		t.Errorf("après restore --staged : %+v", s.Changes)
	}

	commits, err := Log(ctx, dir, 5)
	if err != nil || len(commits) != 1 || commits[0].Subject != "Premier commit" || commits[0].Author != "Test" {
		t.Fatalf("historique : %+v, err=%v", commits, err)
	}
	if out, err := Show(ctx, dir, commits[0].Hash); err != nil || !strings.Contains(out, "Premier commit") || !strings.Contains(out, "+un") {
		t.Errorf("show : %q, err=%v", out, err)
	}

	if _, err := GetStatus(ctx, t.TempDir()); err != ErrNotRepo {
		t.Errorf("hors dépôt : %v", err)
	}
}
