package gomod

import (
	"os"
	"testing"
)

// testdata/ contient des sorties réelles (Go 1.27.3, govulncheck v1.9.0) sur
// un module qui dépend de golang.org/x/text v0.3.7 et github.com/google/uuid
// v1.3.0 ; chemins locaux raccourcis.

func TestParseList(t *testing.T) {
	data, err := os.ReadFile("testdata/golist-u.json")
	if err != nil {
		t.Fatal(err)
	}
	mods, err := ParseList(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 4 || !mods[0].Main || mods[0].Path != "example.com/modfix" {
		t.Fatalf("modules : %d, premier %+v", len(mods), mods[0])
	}
	byPath := map[string]Module{}
	for _, m := range mods {
		byPath[m.Path] = m
	}
	text := byPath["golang.org/x/text"]
	if !text.Direct() || text.Version != "v0.3.7" || text.Update == nil || text.Update.Version != "v0.44.0" {
		t.Errorf("x/text : %+v", text)
	}
	if tools := byPath["golang.org/x/tools"]; tools.Direct() || !tools.Indirect {
		t.Errorf("x/tools doit être indirect : %+v", tools)
	}
	if uuid := byPath["github.com/google/uuid"]; uuid.Update == nil || Kind(uuid.Version, uuid.Update.Version) != Minor {
		t.Errorf("uuid v1.3.0 → v1.6.0 doit être une mise à jour mineure : %+v", uuid.Update)
	}
}

func TestKind(t *testing.T) {
	cases := []struct {
		from, to string
		want     UpdateKind
	}{
		{"v1.2.3", "v1.2.4", Patch},
		{"v1.2.3", "v1.3.0", Minor},
		{"v0.3.7", "v0.44.0", Major}, // v0 : pas de garantie de compatibilité
		{"v0.3.7", "v0.3.8", Patch},
		{"v1.2.3", "v2.0.0", Major},
		{"v1.2.3", "v1.2.3", NoUpdate},
		{"v1.3.0", "v1.2.0", NoUpdate},
		{"v1.2.3", "pas-une-version", NoUpdate},
	}
	for _, c := range cases {
		if got := Kind(c.from, c.to); got != c.want {
			t.Errorf("Kind(%s, %s) = %v, attendu %v", c.from, c.to, got, c.want)
		}
	}
}

func TestParseVulns(t *testing.T) {
	data, err := os.ReadFile("testdata/govulncheck.json")
	if err != nil {
		t.Fatal(err)
	}
	vulns, err := ParseVulns(data)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Vuln{}
	for _, v := range vulns {
		if _, dup := byID[v.ID]; dup {
			t.Errorf("%s apparaît deux fois (les constats doivent être regroupés)", v.ID)
		}
		byID[v.ID] = v
	}
	if len(vulns) != 4 {
		t.Fatalf("%d vulnérabilités, attendu 4 : %v", len(vulns), byID)
	}

	// GO-2026-5970 : fonction vulnérable atteinte via cases → chaîne d'appels.
	v := byID["GO-2026-5970"]
	if v.Level != LevelSymbol || v.Module != "golang.org/x/text" || v.Fixed != "v0.39.0" || len(v.Trace) < 2 {
		t.Errorf("GO-2026-5970 : %+v", v)
	}
	if v.Trace[0].Name() != "golang.org/x/text/unicode/norm.Form.Properties" {
		t.Errorf("première étape de la trace : %q", v.Trace[0].Name())
	}
	if vulns[0].Level != LevelSymbol {
		t.Error("les vulnérabilités appelées doivent venir en premier")
	}
	// GO-2022-1059 : paquet language importé mais fonction non atteinte.
	if v := byID["GO-2022-1059"]; v.Level != LevelPackage || v.Summary == "" || len(v.Trace) != 0 {
		t.Errorf("GO-2022-1059 : %+v", v)
	}
	// Bibliothèque standard.
	if v := byID["GO-2026-6859"]; v.Module != StdlibPath || v.Fixed != "v1.27.4" {
		t.Errorf("GO-2026-6859 : %+v", v)
	}
	if u := byID["GO-2022-1059"].URL(); u != "https://pkg.go.dev/vuln/GO-2022-1059" {
		t.Errorf("URL = %s", u)
	}
}
