package watch

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func write(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func next(t *testing.T, w *Watcher) []string {
	t.Helper()
	select {
	case dirs := <-w.Changes:
		return dirs
	case <-time.After(3 * time.Second):
		t.Fatal("aucun lot reçu")
		return nil
	}
}

func quiet(t *testing.T, w *Watcher) {
	t.Helper()
	select {
	case dirs := <-w.Changes:
		t.Fatalf("lot inattendu : %v", dirs)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWatch(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "a.go"))
	write(t, filepath.Join(root, "b", "b.go"))
	write(t, filepath.Join(root, "vendor", "v", "v.go"))

	w, err := New(root, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Rafale d'écritures dans deux paquets → un seul lot.
	for i := 0; i < 5; i++ {
		write(t, filepath.Join(root, "a", "a.go"))
	}
	write(t, filepath.Join(root, "b", "b_test.go"))
	if got, want := next(t, w), []string{filepath.Join(root, "a"), filepath.Join(root, "b")}; !reflect.DeepEqual(got, want) {
		t.Errorf("lot = %v, attendu %v", got, want)
	}

	// vendor/ et les fichiers non Go sont ignorés.
	write(t, filepath.Join(root, "vendor", "v", "v.go"))
	os.WriteFile(filepath.Join(root, "a", "notes.txt"), []byte("x"), 0o644)
	quiet(t, w)

	// Un répertoire créé après le démarrage est surveillé.
	os.MkdirAll(filepath.Join(root, "c"), 0o755)
	time.Sleep(150 * time.Millisecond) // le temps que le watcher l'ajoute
	write(t, filepath.Join(root, "c", "c.go"))
	if got := next(t, w); !reflect.DeepEqual(got, []string{filepath.Join(root, "c")}) {
		t.Errorf("nouveau répertoire : %v", got)
	}

	// go.mod concerne tout le module.
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644)
	if got := next(t, w); !reflect.DeepEqual(got, []string{AllDirs}) {
		t.Errorf("go.mod : %v", got)
	}

	w.Close()
	if _, ok := <-w.Changes; ok {
		t.Error("Changes doit être fermé après Close")
	}
}
