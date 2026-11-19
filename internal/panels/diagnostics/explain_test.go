package diagnostics

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/diag"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

const apiJSON = `{"Analyzers": [
 {"Name": "S1003", "Doc": "Replace call to strings.Index with strings.Contains\n\nBefore:\n\n    if strings.Index(x, y) != -1 {}\n\nAfter:\n\n    if strings.Contains(x, y) {}\n\nAvailable since\n    2017.1\n", "URL": "https://staticcheck.dev/docs/checks/#S1003"},
 {"Name": "QF1011", "Doc": "Omit redundant type", "URL": "https://staticcheck.dev/docs/checks/#"},
 {"Name": "unusedfunc", "Doc": "check for unused functions", "URL": "https://pkg.go.dev/x/unusedfunc"}
]}`

func TestParseRuleDocs(t *testing.T) {
	docs := parseRuleDocs([]byte(apiJSON))
	if len(docs) != 3 || docs["QF1011"].url != "https://staticcheck.dev/docs/checks/#QF1011" {
		t.Errorf("documentation : %+v", docs)
	}
	if parseRuleDocs([]byte("pas du JSON")) != nil {
		t.Error("JSON invalide")
	}
}

func TestExplain(t *testing.T) {
	t.Setenv("GOPIT_GOPLS", "off")
	old := loadRuleDocs
	loadRuleDocs = func() map[string]ruleDoc { return parseRuleDocs([]byte(apiJSON)) }
	t.Cleanup(func() { loadRuleDocs = old })

	root := t.TempDir()
	file := filepath.Join(root, "a.go")
	m := New(theme.New(), &project.Project{Root: root})
	m.results[diag.ToolStaticcheck] = []diag.Diagnostic{
		{File: file, Line: 3, Code: "S1003", Message: "should use strings.Contains", Severity: diag.Warning},
		{File: file, Line: 5, Message: "function f is unused", Severity: diag.Warning}, // gopls check : pas de règle
	}
	m.merge()
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	msg := cmd() // seule commande : la documentation (pas de gopls pour les corrections)
	if b, ok := msg.(tea.BatchMsg); ok {
		for _, c := range b {
			if c != nil {
				msg = c()
			}
		}
	}
	m.Update(msg.(ruleDocsMsg))
	view := ansi.Strip(m.View(120, 40))
	for _, want := range []string{"Pourquoi", "w : documentation en ligne", "Replace call to strings.Index", "if strings.Contains(x, y) {}"} {
		if !strings.Contains(view, want) {
			t.Errorf("« %s » absent :\n%s", want, view)
		}
	}
	if strings.Contains(view, "Available since") {
		t.Error("version de l'outil : sans intérêt ici")
	}

	// Sans règle : celle du diagnostic de gopls correspondant.
	m.move(1)
	d := m.selected()
	if _, ok := m.explain(d); ok {
		t.Fatal("pas encore de règle connue")
	}
	m.fixes[key(d)] = &fixState{rule: "unusedfunc"}
	if doc, ok := m.explain(d); !ok || doc.url != "https://pkg.go.dev/x/unusedfunc" {
		t.Errorf("règle de gopls : %+v %v", doc, ok)
	}
	m.fixes[key(d)] = &fixState{rule: "compiler"}
	if f, ok := m.openRuleDoc()().(panel.FlashMsg); !ok || f.Error {
		t.Errorf("pas de documentation : %#v", f)
	}

	// gofmt : explication propre à gopit.
	m.results[diag.ToolStaticcheck][0].Code = "gofmt"
	m.merge()
	m.move(-1)
	if doc, ok := m.explain(m.selected()); !ok || !strings.Contains(doc.url, "gofmt") {
		t.Errorf("gofmt : %+v", doc)
	}
}
