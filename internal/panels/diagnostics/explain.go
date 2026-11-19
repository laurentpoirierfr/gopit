package diagnostics

// Explication des règles : la documentation des analyseurs (vet,
// staticcheck, gopls) telle que gopls la publie (« gopls api-json »), avec
// le lien vers la page en ligne. Le texte est celui des outils, en anglais.

import (
	"encoding/json"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/diag"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
)

// ruleDoc est la documentation d'une règle.
type ruleDoc struct {
	doc string
	url string
}

type ruleDocsMsg struct{ docs map[string]ruleDoc }

// loadRuleDocs lit la documentation des analyseurs (remplacée dans les
// tests).
var loadRuleDocs = func() map[string]ruleDoc {
	out, err := exec.Command("gopls", "api-json").Output()
	if err != nil {
		return nil
	}
	return parseRuleDocs(out)
}

func parseRuleDocs(data []byte) map[string]ruleDoc {
	var api struct {
		Analyzers []struct {
			Name, Doc, URL string
		}
	}
	if json.Unmarshal(data, &api) != nil {
		return nil
	}
	docs := make(map[string]ruleDoc, len(api.Analyzers))
	for _, a := range api.Analyzers {
		url := a.URL
		if strings.HasSuffix(url, "#") { // lien incomplet (ex. QF1011)
			url += a.Name
		}
		docs[a.Name] = ruleDoc{doc: a.Doc, url: url}
	}
	return docs
}

// ensureRuleDocs charge la documentation des règles, une fois.
func (m *Model) ensureRuleDocs() tea.Cmd {
	if m.ruleDocs != nil {
		return nil
	}
	m.ruleDocs = map[string]ruleDoc{} // chargement en cours
	return func() tea.Msg { return ruleDocsMsg{loadRuleDocs()} }
}

// rule renvoie la règle d'un diagnostic : la sienne, ou à défaut celle du
// diagnostic de gopls correspondant (trouvée avec les corrections).
func (m *Model) rule(d *diag.Diagnostic) string {
	if d.Code != "" && d.Code != "compilation" {
		return d.Code
	}
	if st := m.fixes[key(d)]; st != nil && st.rule != "compiler" {
		return st.rule
	}
	return ""
}

// explain renvoie l'explication de la règle du diagnostic d.
func (m *Model) explain(d *diag.Diagnostic) (ruleDoc, bool) {
	r := m.rule(d)
	if r == "gofmt" {
		return ruleDoc{doc: i18n.T("gofmt met le code dans la forme standard de Go : indentation par tabulations, espaces autour des opérateurs, alignement des commentaires et des champs. Tout le code Go est ainsi présenté de la même façon. L'éditeur de gopit formate les fichiers Go à l'enregistrement."),
			url: "https://pkg.go.dev/cmd/gofmt"}, true
	}
	doc, ok := m.ruleDocs[r]
	return doc, ok && r != ""
}

// viewExplain renvoie la section « Pourquoi » : le début de la
// documentation de la règle (exemples compris), au plus max lignes.
func (m *Model) viewExplain(d *diag.Diagnostic, w, max int) []string {
	doc, ok := m.explain(d)
	if !ok {
		return nil
	}
	t := m.th
	head := t.Label.Render(i18n.T("Pourquoi"))
	if doc.url != "" {
		head += "  " + panel.Zone("doc:web", t.Dim.Render(i18n.T("w : documentation en ligne")))
	}
	out := []string{ansi.Truncate(head, w, "…")}
	text := doc.doc
	if i := strings.Index(text, "\nAvailable since"); i >= 0 {
		text = text[:i]
	}
	wrap := lipgloss.NewStyle().Width(w)
	code := lipgloss.NewStyle().Foreground(t.P.Peach)
	var body []string
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.HasPrefix(l, "    ") || strings.HasPrefix(l, "\t") { // exemple de code
			body = append(body, ansi.Truncate(code.Render("  "+strings.TrimSpace(strings.ReplaceAll(l, "\t", "    "))), w, "…"))
			continue
		}
		body = append(body, strings.Split(wrap.Render(t.Text.Render(l)), "\n")...)
	}
	if len(body) > max {
		body = append(body[:max-1], t.Dim.Render(i18n.T("… la suite en ligne (w)")))
	}
	return append(out, body...)
}

// openRuleDoc ouvre la documentation en ligne de la règle choisie.
func (m *Model) openRuleDoc() tea.Cmd {
	d := m.selected()
	if d == nil {
		return nil
	}
	doc, ok := m.explain(d)
	if !ok || doc.url == "" {
		return panel.Flash(i18n.T("Pas de documentation pour cette règle"), false)
	}
	return panel.Browse(doc.url)
}
