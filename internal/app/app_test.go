package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/config"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
)

const width, height = 120, 40

// newApp crée le cockpit sur un petit module temporaire, avec des
// préférences rangées dans un répertoire temporaire. Les commandes lancées
// par les modules (go test, git…) ne sont pas exécutées.
func newApp(t *testing.T, cfg *config.Config) *Model {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GOPIT_GOPLS", "off") // pas de connexion à gopls
	i18n.Set(i18n.FR)
	t.Cleanup(func() { i18n.Set(i18n.FR) })
	if cfg != nil {
		if err := cfg.Save(); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	for name, src := range map[string]string{
		"go.mod": "module exemple\n\ngo 1.22\n",
		"a.go":   "package exemple\n\nvar X = 1\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := project.Detect(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := New(p)
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return m
}

// key fabrique une touche à partir de son nom (format de KeyPressMsg.String).
func key(s string) tea.KeyPressMsg {
	special := map[string]rune{
		"esc": tea.KeyEscape, "enter": tea.KeyEnter, "right": tea.KeyRight, "left": tea.KeyLeft,
		"up": tea.KeyUp, "down": tea.KeyDown, "f1": tea.KeyF1, "f10": tea.KeyF10, "tab": tea.KeyTab,
	}
	if c, ok := special[s]; ok {
		return tea.KeyPressMsg{Code: c}
	}
	if c, ok := strings.CutPrefix(s, "ctrl+"); ok {
		return tea.KeyPressMsg{Code: []rune(c)[0], Mod: tea.ModCtrl}
	}
	if c, ok := strings.CutPrefix(s, "alt+"); ok {
		return tea.KeyPressMsg{Code: []rune(c)[0], Mod: tea.ModAlt}
	}
	return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
}

// press envoie des touches et renvoie la commande de la dernière.
func press(m *Model, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = m.Update(key(k))
	}
	return cmd
}

// typeText envoie un texte lettre par lettre.
func typeText(m *Model, s string) {
	for _, r := range s {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func isQuit(cmd tea.Cmd) bool {
	return cmd != nil && reflect.ValueOf(cmd).Pointer() == reflect.ValueOf(tea.Quit).Pointer()
}

// panelIndex renvoie la place du module de ce titre (titre français).
func panelIndex(t *testing.T, m *Model, title string) int {
	t.Helper()
	for i, p := range m.panels {
		if p.Title() == title {
			return i
		}
	}
	t.Fatalf("module %q introuvable", title)
	return -1
}

// openInEditor ouvre a.go dans l'éditeur intégré, en édition.
func openInEditor(t *testing.T, m *Model) {
	t.Helper()
	if _, cmd := m.Update(panel.OpenMsg{File: "a.go", Line: 3}); cmd != nil {
		t.Fatalf("ouverture : %v", cmd())
	}
	if m.cur != panelIndex(t, m, "Éditeur") {
		t.Fatalf("module affiché : %s", m.panels[m.cur].Title())
	}
}

func savedConfig(t *testing.T) *config.Config {
	t.Helper()
	p, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("préférences non enregistrées : %v", err)
	}
	var c config.Config
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return &c
}

func TestQuitSavesPreferences(t *testing.T) {
	m := newApp(t, nil)
	press(m, "5") // Git
	if !isQuit(press(m, "q")) {
		t.Fatal("q quitte")
	}
	if c := savedConfig(t); c.LastModule != "Git" {
		t.Errorf("dernier module : %q", c.LastModule)
	}
}

func TestQuitWithUnsavedFiles(t *testing.T) {
	m := newApp(t, nil)
	openInEditor(t, m)
	typeText(m, "q") // en édition, q s'écrit dans le texte
	if !m.panels[m.cur].(panel.Capturer).Capturing() || len(m.unsaved()) != 1 {
		t.Fatalf("fichier modifié attendu : %v", m.unsaved())
	}

	if isQuit(press(m, "ctrl+q")) || !m.quitArmed || !strings.Contains(m.flash, "a.go") {
		t.Fatalf("1er Ctrl+Q : avertissement attendu (%q)", m.flash)
	}
	press(m, "f1", "esc") // une autre touche désarme
	if m.quitArmed {
		t.Fatal("une autre touche annule la confirmation")
	}
	press(m, "ctrl+q")
	if !isQuit(press(m, "ctrl+q")) {
		t.Fatal("2e Ctrl+Q de suite : quitter")
	}
}

func TestCtrlC(t *testing.T) {
	m := newApp(t, nil)
	openInEditor(t, m)
	if isQuit(press(m, "ctrl+c")) {
		t.Fatal("dans l'éditeur, Ctrl+C copie")
	}
	press(m, "esc") // lecture : copie encore
	if isQuit(press(m, "ctrl+c")) {
		t.Fatal("en lecture aussi, Ctrl+C copie")
	}
	press(m, "1") // Accueil
	if !isQuit(press(m, "ctrl+c")) {
		t.Fatal("hors de l'éditeur, Ctrl+C quitte")
	}

	m = newApp(t, nil)
	openInEditor(t, m)
	press(m, "ctrl+p")
	if !isQuit(press(m, "ctrl+c")) {
		t.Fatal("palette ouverte : Ctrl+C quitte")
	}
}

func TestSwitchModule(t *testing.T) {
	m := newApp(t, nil)
	// Ordre d'usage : Accueil, Éditeur, Tests, Diagnostics, Git, Terminal,
	// Docs, Modules, Benchmarks.
	for key, want := range map[string]string{"2": "Éditeur", "3": "Tests", "5": "Git", "6": "Terminal", "9": "Benchmarks"} {
		press(m, "1") // retour à l'Accueil (le Terminal capte les touches)
		press(m, key)
		if got := m.panels[m.cur].Title(); got != want {
			t.Errorf("%s : %s, attendu %s", key, got, want)
		}
		if want == "Terminal" {
			press(m, "esc") // sortir de la saisie
		}
	}
	press(m, "3")
	openInEditor(t, m)
	typeText(m, "3")
	if m.panels[m.cur].Title() != "Éditeur" {
		t.Error("en édition, un chiffre s'écrit dans le texte")
	}
}

func TestPalette(t *testing.T) {
	m := newApp(t, nil)
	press(m, "ctrl+p")
	if !m.pal.open || len(m.pal.matches) == 0 {
		t.Fatal("Ctrl+P ouvre la palette")
	}
	typeText(m, "aller git")
	if len(m.pal.matches) != 1 {
		t.Fatalf("filtre « aller git » : %d résultats", len(m.pal.matches))
	}
	press(m, "enter")
	if m.pal.open || m.panels[m.cur].Title() != "Git" {
		t.Errorf("Entrée exécute : palette %v, module %s", m.pal.open, m.panels[m.cur].Title())
	}
}

func TestMenusAndHelp(t *testing.T) {
	m := newApp(t, nil)
	press(m, "f10")
	if m.menu.open != 0 {
		t.Fatalf("F10 ouvre le premier menu : %d", m.menu.open)
	}
	press(m, "left") // tourne jusqu'au dernier menu
	if n := len(m.menus()); m.menu.open != n-1 {
		t.Errorf("← depuis le premier : %d / %d", m.menu.open, n)
	}
	press(m, "esc")
	if m.menu.open != -1 {
		t.Error("Échap ferme le menu")
	}

	// Affichage → Barre latérale des modules (premier élément).
	sidebar := m.sidebar
	press(m, "alt+a", "enter")
	if m.sidebar == sidebar || m.menu.open != -1 {
		t.Errorf("menu Affichage : barre latérale %v → %v", sidebar, m.sidebar)
	}

	press(m, "f1")
	if m.modal != modalHelp {
		t.Fatal("F1 ouvre l'aide")
	}
	cur := m.cur
	press(m, "3") // n'importe quelle touche ferme l'aide, sans agir
	if m.modal != modalNone || m.cur != cur {
		t.Errorf("touche dans l'aide : fenêtre %v, module %d → %d", m.modal, cur, m.cur)
	}
}

func TestLanguage(t *testing.T) {
	m := newApp(t, nil)
	press(m, "ctrl+p")
	typeText(m, "english")
	press(m, "enter")
	if i18n.Current() != i18n.EN {
		t.Fatalf("langue : %s", i18n.Current())
	}
	if !strings.Contains(ansi.Strip(m.render()), "Home") {
		t.Error("interface en anglais attendue")
	}
	if !isQuit(press(m, "q")) {
		t.Fatal("q quitte")
	}
	if c := savedConfig(t); c.Language != "en" {
		t.Errorf("langue enregistrée : %q", c.Language)
	}
}

func TestLanguageNotSavedUnlessChosen(t *testing.T) {
	m := newApp(t, &config.Config{Language: "es"})
	i18n.Set(i18n.EN) // -lang en : pour cette session seulement
	press(m, "q")
	if c := savedConfig(t); c.Language != "es" {
		t.Errorf("langue des préférences écrasée : %q", c.Language)
	}
}

func TestRestorePreferences(t *testing.T) {
	m := newApp(t, &config.Config{LastModule: "Docs", Sidebar: true, HideHints: true})
	if m.panels[m.cur].Title() != "Docs" || !m.sidebar || !m.hideHints {
		t.Errorf("module %s, barre latérale %v, raccourcis masqués %v", m.panels[m.cur].Title(), m.sidebar, m.hideHints)
	}
}

// TestRenderFits vérifie que l'écran occupe exactement le terminal, dans
// chaque langue et chaque module : un texte traduit plus long ne doit pas
// déborder.
func TestRenderFits(t *testing.T) {
	for _, l := range i18n.Langs {
		for _, sidebar := range []bool{false, true} {
			m := newApp(t, &config.Config{Sidebar: sidebar})
			i18n.Set(l)
			for i := range m.panels {
				m.cur = i
				for _, extra := range []string{"", "f1", "f10", "ctrl+p"} {
					if extra != "" {
						press(m, extra)
					}
					out := m.render()
					lines := strings.Split(out, "\n")
					if len(lines) != height {
						t.Errorf("%s, %s, %q : %d lignes", l, m.panels[i].Title(), extra, len(lines))
					}
					for n, line := range lines {
						if w := ansi.StringWidth(line); w > width {
							t.Errorf("%s, %s, %q : ligne %d de largeur %d", l, m.panels[i].Title(), extra, n, w)
							break
						}
					}
					press(m, "esc")
				}
			}
		}
	}
}

func TestAccelMenu(t *testing.T) {
	mn := accelMenu("&Édition", nil)
	if mn.title != "Édition" || mn.hotkey != "alt+e" || mn.accel != 0 {
		t.Errorf("%+v", mn)
	}
	if mn := accelMenu("A&yuda", nil); mn.title != "Ayuda" || mn.hotkey != "alt+y" || mn.accel != 1 {
		t.Errorf("%+v", mn)
	}
	if mn := accelMenu("Sans", nil); mn.hotkey != "" || mn.accel != -1 {
		t.Errorf("%+v", mn)
	}
}

// TestMenuHotkeysPerLanguage vérifie que, dans chaque langue, les lettres
// des menus sont toutes différentes et ouvrent le bon menu.
func TestMenuHotkeysPerLanguage(t *testing.T) {
	m := newApp(t, nil)
	for _, l := range i18n.Langs {
		i18n.Set(l)
		seen := map[string]string{}
		for i, mn := range m.menus() {
			if mn.hotkey == "" {
				continue
			}
			if other, dup := seen[mn.hotkey]; dup {
				t.Errorf("%s : %s pour « %s » et « %s »", l, mn.hotkey, other, mn.title)
			}
			seen[mn.hotkey] = mn.title
			if r := []rune(mn.title); mn.accel >= len(r) || foldLetter(r[mn.accel]) != strings.TrimPrefix(mn.hotkey, "alt+") {
				t.Errorf("%s : lettre soulignée de « %s » ≠ %s", l, mn.title, mn.hotkey)
			}
			m.Update(key(mn.hotkey))
			if m.menu.open != i {
				t.Errorf("%s : %s ouvre le menu %d, attendu %d (%s)", l, mn.hotkey, m.menu.open, i, mn.title)
			}
			press(m, "esc")
		}
		if len(seen) != 5 {
			t.Errorf("%s : %d menus avec raccourci", l, len(seen))
		}
	}
}

func TestLanguageChangeRefreshesModules(t *testing.T) {
	m := newApp(t, nil)
	press(m, "ctrl+p")
	typeText(m, "español")
	if cmd := press(m, "enter"); cmd == nil {
		t.Fatal("changer de langue prévient les modules (commande attendue)")
	}
	if i18n.Current() != i18n.ES || !strings.Contains(ansi.Strip(m.render()), "Ayuda") {
		t.Error("interface en espagnol attendue")
	}
}
