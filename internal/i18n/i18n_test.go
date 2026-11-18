package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// messages relève les messages passés à T, N et Error dans le code du module.
func messages(t *testing.T) map[string]string {
	t.Helper()
	msgs := map[string]string{} // message → premier emplacement
	fset := token.NewFileSet()
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			var name string
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr: // i18n.T(…) hors du paquet
				if id, ok := fn.X.(*ast.Ident); ok && id.Name == "i18n" {
					name = fn.Sel.Name
				}
			case *ast.Ident: // T(…) dans le paquet
				if f.Name.Name == "i18n" {
					name = fn.Name
				}
			}
			if name != "T" && name != "N" && name != "Error" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, _ := strconv.Unquote(lit.Value)
			if _, seen := msgs[s]; !seen {
				msgs[s] = fset.Position(lit.Pos()).String()
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) < 100 {
		t.Fatalf("seulement %d messages trouvés : le parcours du code a échoué ?", len(msgs))
	}
	return msgs
}

var verbRe = regexp.MustCompile(`%[-+# 0]*[0-9.*]*[a-zA-Z%]`)

// TestCatalogs vérifie que chaque message a sa traduction anglaise et
// espagnole, avec les mêmes verbes de format, et qu'aucune traduction n'est
// orpheline.
func TestCatalogs(t *testing.T) {
	msgs := messages(t)
	for _, l := range []Lang{EN, ES} {
		cat := catalogs[l]
		var missing []string
		for m, pos := range msgs {
			tr, ok := cat[m]
			if !ok {
				missing = append(missing, pos+" "+strconv.Quote(m))
				continue
			}
			if a, b := verbRe.FindAllString(m, -1), verbRe.FindAllString(tr, -1); !slices.Equal(a, b) {
				t.Errorf("%s : verbes de format différents pour %q : %v / %v", l, m, a, b)
			}
		}
		sort.Strings(missing)
		for _, m := range missing {
			t.Errorf("%s : traduction manquante : %s", l, m)
		}
		for m := range cat {
			if _, ok := msgs[m]; !ok {
				t.Errorf("%s : traduction inutilisée : %q", l, m)
			}
		}
	}
}

func TestSwitch(t *testing.T) {
	defer Set(FR)
	Set(EN)
	if got := T("Quitter"); got != "Quit" {
		t.Errorf("anglais : %q", got)
	}
	if got := Key("Maj+F3 / Entrée"); got != "Shift+F3 / Enter" {
		t.Errorf("touche : %q", got)
	}
	err := Error("interrompu")
	Set(ES)
	if got := err.Error(); got != "interrumpido" {
		t.Errorf("erreur traduite à la lecture : %q", got)
	}
	Set("de")
	if Current() != FR || T("Quitter") != "Quitter" {
		t.Error("langue inconnue : français")
	}
}

func TestParse(t *testing.T) {
	for s, want := range map[string]Lang{"fr_FR.UTF-8": FR, "en": EN, "es-MX": ES, "EN_us": EN} {
		if l, ok := Parse(s); !ok || l != want {
			t.Errorf("%q : %q", s, l)
		}
	}
	for _, s := range []string{"de_DE", "C", "", "frx"} {
		if l, ok := Parse(s); ok {
			t.Errorf("%q : %q, attendu inconnu", s, l)
		}
	}
}
