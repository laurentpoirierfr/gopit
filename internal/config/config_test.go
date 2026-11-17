package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSave(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	c, err := Load()
	if err != nil || c.LastModule != "" || c.Panels == nil {
		t.Fatalf("configuration absente : %+v, err=%v", c, err)
	}
	c.LastModule = "Tests"
	c.Panels["Tests"] = map[string]string{"cover": "true"}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	p, _ := Path()
	if filepath.Base(filepath.Dir(p)) != "gopit" {
		t.Errorf("emplacement : %s", p)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Error("le fichier temporaire doit être renommé")
	}

	c2, err := Load()
	if err != nil || c2.LastModule != "Tests" || c2.Panels["Tests"]["cover"] != "true" {
		t.Errorf("relu : %+v, err=%v", c2, err)
	}

	// Fichier abîmé : configuration vide et erreur.
	os.WriteFile(p, []byte("{"), 0o644)
	if c3, err := Load(); err == nil || c3.LastModule != "" || c3.Panels == nil {
		t.Errorf("fichier abîmé : %+v, err=%v", c3, err)
	}
}
