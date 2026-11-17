package project

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDetect(t *testing.T) {
	root := t.TempDir()
	gomod := `module example.com/demo/outil

go 1.24

toolchain go1.27.3

require (
	example.com/a v1.0.0
	example.com/b v1.2.0 // indirect
	example.com/c v0.3.0
)
`
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "internal", "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	// Depuis un sous-répertoire, Detect doit remonter jusqu'au go.mod.
	p, err := Detect(sub)
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != root || p.Module != "example.com/demo/outil" || p.Name() != "outil" {
		t.Errorf("projet inattendu : %+v", p)
	}
	if p.GoVersion != "1.24" || p.Toolchain != "go1.27.3" {
		t.Errorf("versions : go=%q toolchain=%q", p.GoVersion, p.Toolchain)
	}
	if p.Requires != 2 || p.Indirect != 1 {
		t.Errorf("dépendances : %d directes, %d indirectes", p.Requires, p.Indirect)
	}
}

func TestDetectNoModule(t *testing.T) {
	if _, err := Detect(t.TempDir()); !errors.Is(err, ErrNoModule) {
		t.Errorf("ErrNoModule attendue, obtenu %v", err)
	}
}
