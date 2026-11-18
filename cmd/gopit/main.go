// gopit : le cockpit du développeur Go dans le terminal.
//
// Usage : gopit [répertoire]   (par défaut le répertoire courant)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"

	"github.com/laurentpoirierfr/gopit/internal/app"
	"github.com/laurentpoirierfr/gopit/internal/config"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
	"github.com/laurentpoirierfr/gopit/internal/newmod"
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/version"
)

func main() {
	// Langue : celle des préférences, sinon celle du système ; -lang l'emporte.
	i18n.Set(i18n.Detect())
	if cfg, _ := config.Load(); cfg != nil {
		if l, ok := i18n.Parse(cfg.Language); ok {
			i18n.Set(l)
		}
	}
	lang := flag.String("lang", "", i18n.T("langue de l'interface : fr, en ou es"))
	showVersion := flag.Bool("version", false, i18n.T("affiche la version et quitte"))

	flag.Usage = func() {
		fmt.Fprint(os.Stderr, i18n.T("Usage : gopit [options] [répertoire]\n\n"))
		fmt.Fprint(os.Stderr, i18n.T("Ouvre le cockpit sur le module Go qui contient le répertoire\n(par défaut le répertoire courant).\n\nOptions :\n"))
		flag.PrintDefaults()
	}
	flag.Parse()
	if *lang != "" {
		l, ok := i18n.Parse(*lang)
		if !ok {
			fmt.Fprintln(os.Stderr, "gopit:", i18n.T("langue inconnue (fr, en ou es) : ")+*lang)
			os.Exit(2)
		}
		i18n.Set(l)
	}

	if *showVersion {
		fmt.Println("gopit", version.Version)
		return
	}

	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}
	proj, err := project.Detect(dir)
	if errors.Is(err, project.ErrNoModule) && term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) {
		proj, err = createModule(dir, err)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gopit:", err)
		os.Exit(1)
	}

	if _, err := tea.NewProgram(app.New(proj)).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "gopit:", err)
		os.Exit(1)
	}
}

// createModule propose de créer un module Go dans dir (aucun go.mod trouvé
// en remontant) ; renoncer renvoie l'erreur d'origine.
func createModule(dir string, notFound error) (*project.Project, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, notFound
	}
	path, ok, err := newmod.Ask(abs, newmod.DefaultPath(abs))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, notFound
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := newmod.Create(ctx, abs, path); err != nil {
		return nil, err
	}
	fmt.Printf(i18n.T("✓ go.mod créé : module %s")+"\n", path)
	return project.Detect(abs)
}
