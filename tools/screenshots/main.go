// screenshots produit les captures d'écran de la documentation : il pilote
// gopit sur son propre dépôt, sans terminal (comme le ferait Bubble Tea :
// les commandes s'exécutent, leurs messages reviennent au modèle), met
// chaque module dans un état parlant, puis enregistre l'écran en PNG.
//
//	go run ./tools/screenshots            (ou : make screenshots)
//	go run ./tools/screenshots -only 2,5  (seulement l'Éditeur et le Débogage)
//
// Il faut une police Nerd Font (icônes), gopls, Delve et git, comme pour
// utiliser gopit.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/laurentpoirierfr/gopit/internal/app"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/panel"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/theme"
)

func main() {
	home, _ := os.UserHomeDir()
	out := flag.String("o", "assets/screenshots", "répertoire des images")
	fontDir := flag.String("font-dir", filepath.Join(home, ".local/share/fonts/JetBrainsMonoNerdFont"), "répertoire de la police")
	fontName := flag.String("font", "JetBrainsMonoNerdFontMono", "nom de la police (fichiers <nom>-Regular.ttf, -Bold, -Italic)")
	size := flag.Float64("size", 24, "taille de la police en pixels")
	cols := flag.Int("cols", 150, "colonnes")
	rows := flag.Int("rows", 42, "lignes")
	only := flag.String("only", "", "touches des modules à capturer (ex. 2,5), tous par défaut")
	flag.Parse()

	if err := run(*out, *fontDir, *fontName, *size, *cols, *rows, *only); err != nil {
		fmt.Fprintln(os.Stderr, "screenshots :", err)
		os.Exit(1)
	}
}

func run(out, fontDir, fontName string, size float64, cols, rows int, only string) error {
	proj, err := project.Detect(".")
	if err != nil {
		return err
	}
	// Préférences et historiques neufs : barre latérale affichée, en français.
	tmp, err := os.MkdirTemp("", "gopit-screenshots-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	for k, v := range map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(tmp, "config"), "XDG_CACHE_HOME": filepath.Join(tmp, "cache"),
		"GOPIT_WATCH": "off", "SHELL": "/bin/sh", "PS1": "$ ", "LANG": "fr_FR.UTF-8",
		"IMGVIEW_NO_KITTY": "1", // images en demi-blocs : le rendu PNG ne connaît pas le protocole Kitty
	} {
		os.Setenv(k, v)
	}
	cfgDir := filepath.Join(tmp, "config", "gopit")
	os.MkdirAll(cfgDir, 0o755)
	os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"sidebar": true, "language": "fr"}`), 0o600)
	i18n.Set(i18n.FR)

	th := theme.New()
	th.Set(true)
	r, err := newRenderer(fontDir, fontName, size, th.P.Text, th.P.Base)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	d := &driver{m: app.New(proj), msgs: make(chan tea.Msg, 256), root: proj.Root}
	d.send(tea.WindowSizeMsg{Width: cols, Height: rows})
	d.send(tea.BackgroundColorMsg{Color: color.Black}) // thème sombre
	d.run(d.m.Init())

	shot := func(name string) error {
		d.settle(400 * time.Millisecond)
		img := frame(r.terminal(d.screen(), cols, rows), r.bg, size/20)
		f, err := os.Create(filepath.Join(out, name+".png"))
		if err != nil {
			return err
		}
		defer f.Close()
		fmt.Println("✓", f.Name())
		return png.Encode(f, img)
	}

	for _, s := range scenarios {
		if only != "" && !strings.Contains(","+only+",", ","+s.key+",") {
			continue
		}
		fmt.Printf("… %s\n", s.name)
		d.pump(3200 * time.Millisecond) // les messages du module précédent s'effacent
		if err := s.run(d, shot); err != nil {
			fmt.Fprintf(os.Stderr, "  %s : %v\n", s.name, err)
		}
	}
	return nil
}

// ─── Pilotage ────────────────────────────────────────────────────────────────

type driver struct {
	m    *app.Model
	msgs chan tea.Msg
	root string
}

// run exécute une commande en arrière-plan ; son message revient par msgs.
func (d *driver) run(c tea.Cmd) {
	if c != nil {
		go func() { d.msgs <- c() }()
	}
}

// send remet un message au modèle (et lance les commandes qui en découlent).
func (d *driver) send(msg tea.Msg) {
	switch msg := msg.(type) {
	case nil, tea.QuitMsg:
	case tea.BatchMsg:
		for _, c := range msg {
			d.run(c)
		}
	default:
		_, c := d.m.Update(msg)
		d.run(c)
	}
}

func (d *driver) screen() string { return d.m.View().Content }

func (d *driver) text() string { return ansi.Strip(d.screen()) }

// waitFor traite les messages jusqu'à ce que cond soit vraie sur le texte
// de l'écran.
func (d *driver) waitFor(what string, timeout time.Duration, cond func(string) bool) error {
	deadline := time.After(timeout)
	for !cond(d.text()) {
		select {
		case msg := <-d.msgs:
			d.send(msg)
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			return fmt.Errorf("délai dépassé : %s", what)
		}
	}
	return nil
}

// settle traite les messages jusqu'à un moment de calme, sans attendre plus
// de 3 secondes (une animation envoie des messages en continu).
func (d *driver) settle(quiet time.Duration) {
	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-d.msgs:
			d.send(msg)
		case <-time.After(quiet):
			return
		case <-deadline:
			return
		}
	}
}

// pump fait tourner l'application pendant une durée fixe.
func (d *driver) pump(dur time.Duration) {
	deadline := time.After(dur)
	for {
		select {
		case msg := <-d.msgs:
			d.send(msg)
		case <-deadline:
			return
		}
	}
}

var keyCodes = map[string]rune{
	"enter": tea.KeyEnter, "esc": tea.KeyEscape, "up": tea.KeyUp, "down": tea.KeyDown, "end": tea.KeyEnd,
	"home": tea.KeyHome, "f5": tea.KeyF5, "f6": tea.KeyF6, "f9": tea.KeyF9, "space": tea.KeySpace,
}

// key envoie une touche (« enter », « ctrl+] », « shift+f5 », « a »).
func (d *driver) key(s string) {
	var k tea.KeyPressMsg
	name := s
	for {
		switch {
		case strings.HasPrefix(name, "ctrl+") && len(name) > 5:
			k.Mod |= tea.ModCtrl
			name = name[5:]
			continue
		case strings.HasPrefix(name, "alt+") && len(name) > 4:
			k.Mod |= tea.ModAlt
			name = name[4:]
			continue
		case strings.HasPrefix(name, "shift+") && len(name) > 6:
			k.Mod |= tea.ModShift
			name = name[6:]
			continue
		}
		break
	}
	if c, ok := keyCodes[name]; ok {
		k.Code = c
	} else {
		k.Code = []rune(name)[0]
		if k.Mod == 0 {
			k.Text = name
		}
	}
	d.send(k)
}

func (d *driver) typeText(s string) {
	for _, r := range s {
		if r == '\r' {
			d.key("enter")
			continue
		}
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// lineOf renvoie le numéro (à partir de 1) de la première ligne de file qui
// contient text.
func (d *driver) lineOf(file, text string) int {
	f, err := os.Open(filepath.Join(d.root, file))
	if err != nil {
		return 1
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		if strings.Contains(sc.Text(), text) {
			return n
		}
	}
	return 1
}

// edit insère text après la première occurrence de after dans file (relatif
// au dépôt) et renvoie de quoi remettre le fichier tel qu'il était.
func (d *driver) edit(file, after, text string) (restore func(), err error) {
	path := filepath.Join(d.root, file)
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	orig, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	i := strings.Index(string(orig), after)
	if i < 0 {
		return nil, fmt.Errorf("%s : « %s » introuvable", file, strings.TrimSpace(after))
	}
	i += len(after)
	changed := string(orig[:i]) + text + string(orig[i:])
	if err := os.WriteFile(path, []byte(changed), st.Mode().Perm()); err != nil {
		return nil, err
	}
	return func() { _ = os.WriteFile(path, orig, st.Mode().Perm()) }, nil
}

func (d *driver) open(file, text string) {
	d.send(panel.OpenMsg{File: filepath.Join(d.root, file), Line: d.lineOf(file, text)})
}

// ─── Scénarios ───────────────────────────────────────────────────────────────

type scenario struct {
	key, name string
	run       func(d *driver, shot func(string) error) error
}

func contains(subs ...string) func(string) bool {
	return func(s string) bool {
		for _, sub := range subs {
			if !strings.Contains(s, sub) {
				return false
			}
		}
		return true
	}
}

var scenarios = []scenario{
	{"1", "Accueil", func(d *driver, shot func(string) error) error {
		d.key("1")
		if err := d.waitFor("résumé du module", time.Minute, contains("fichiers sources", "paquets")); err != nil {
			return err
		}
		return shot("01-accueil")
	}},
	{"2", "Éditeur", func(d *driver, shot func(string) error) error {
		// Complétion de gopls dans un vrai fichier de gopit.
		d.open("internal/textbuf/textbuf.go", "w := []rune(with)")
		d.key("end")
		d.key("enter")
		d.typeText("strings.")
		// gopls démarre avec le premier fichier Go : redemander la complétion
		// (Ctrl+Espace) jusqu'à ce qu'il réponde.
		var err error
		for range 12 {
			if err = d.waitFor("complétion de gopls", 10*time.Second, contains("Contains", "ReplaceAll")); err == nil {
				break
			}
			d.key("ctrl+space")
		}
		if err == nil {
			err = shot("02-editeur")
		}
		d.key("esc")
		for range 3 {
			d.key("ctrl+z")
		}
		d.key("esc")
		return err
	}},
	{"3", "Tests", func(d *driver, shot func(string) error) error {
		d.key("3")
		d.key("v") // couverture
		d.key("a")
		done := regexp.MustCompile(`tous les tests · \d`)
		if err := d.waitFor("fin des tests", 8*time.Minute, done.MatchString); err != nil {
			return err
		}
		return shot("03-tests")
	}},
	{"4", "Diagnostics", func(d *driver, shot func(string) error) error {
		d.key("4")
		if err := d.waitFor("fin de l'analyse", 5*time.Minute, func(s string) bool {
			return strings.Contains(s, "Emplacement") || strings.Contains(s, "Aucun problème trouvé")
		}); err != nil {
			return err
		}
		return shot("04-diagnostics")
	}},
	{"5", "Débogage", func(d *driver, shot func(string) error) error {
		// Point d'arrêt au démarrage de gopit lui-même, avant son interface.
		d.open("cmd/gopit/main.go", "proj, err := project.Detect(dir)")
		d.key("esc")
		d.key("f9")
		d.key("f5")
		if err := d.waitFor("arrêt au point d'arrêt", 3*time.Minute, contains("▶")); err != nil {
			return err
		}
		if err := shot("05-debogage-editeur"); err != nil {
			return err
		}
		d.key("5")
		err := d.waitFor("pile et variables", time.Minute, contains("En pause", "main.main", "Locals"))
		if err == nil {
			err = shot("05-debogage")
		}
		d.key("shift+f5")
		d.settle(time.Second)
		return err
	}},
	{"6", "Git", func(d *driver, shot func(string) error) error {
		// Une modification en cours, pour montrer un diff (et non le détail
		// d'un commit, qui afficherait l'adresse de l'auteur) ; le fichier
		// est remis tel quel ensuite.
		restore, err := d.edit("CHANGELOG.md", "## [Non publié]\n\n### Ajouté\n\n",
			"- Une nouvelle entrée, en cours de rédaction.\n")
		if err != nil {
			return err
		}
		defer restore()
		d.key("6")
		d.key("r") // relire l'état du dépôt
		if err := d.waitFor("fichier modifié", time.Minute, contains("Modifiés (1)", "CHANGELOG.md")); err != nil {
			return err
		}
		d.key("home") // sélection sur le fichier modifié
		if err := d.waitFor("diff du fichier", time.Minute, contains("diff --git a/CHANGELOG.md", "en cours de rédaction")); err != nil {
			return err
		}
		if strings.Contains(d.text(), "@") && strings.Contains(d.text(), "<") && strings.Contains(d.text(), ">") &&
			regexp.MustCompile(`<[^>\s]+@[^>\s]+>`).MatchString(d.text()) {
			return fmt.Errorf("une adresse e-mail est visible : capture abandonnée")
		}
		return shot("06-git")
	}},
	{"7", "Terminal", func(d *driver, shot func(string) error) error {
		d.key("7")
		if err := d.waitFor("invite du shell", 30*time.Second, contains("$ ")); err != nil {
			return err
		}
		d.typeText("go version && git log --oneline --decorate -8\r")
		err := d.waitFor("sortie des commandes", 30*time.Second, contains("go version go"))
		if err == nil {
			err = shot("07-terminal")
		}
		d.key("ctrl+]") // rendre la main à gopit
		return err
	}},
	{"8", "Docs", func(d *driver, shot func(string) error) error {
		d.key("8")
		if err := d.waitFor("liste des paquets", time.Minute, contains("paquets du module")); err != nil {
			return err
		}
		d.key("/")
		d.typeText("textbuf")
		d.key("enter")
		d.key("enter")
		if err := d.waitFor("documentation du paquet", time.Minute, contains("ReplaceAll", "Buffer")); err != nil {
			return err
		}
		return shot("08-docs")
	}},
	{"9", "Modules", func(d *driver, shot func(string) error) error {
		d.key("9")
		if err := d.waitFor("dépendances", time.Minute, contains("Directes")); err != nil {
			return err
		}
		return shot("09-modules")
	}},
	{"0", "Benchmarks", func(d *driver, shot func(string) error) error {
		d.key("0")
		d.key("a")
		if err := d.waitFor("mesures", 10*time.Minute, contains("1 exécution(s) enregistrée(s)")); err != nil {
			return err
		}
		return shot("10-benchmarks")
	}},
	{"a", "À propos", func(d *driver, shot func(string) error) error {
		d.key("1")
		d.key("alt+d") // menu Aide
		d.key("down")
		d.key("enter")
		if err := d.waitFor("fenêtre À propos", 10*time.Second, contains("Auteur")); err != nil {
			return err
		}
		err := shot("11-a-propos")
		d.key("esc")
		return err
	}},
}
