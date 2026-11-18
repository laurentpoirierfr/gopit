// gopit : le cockpit du développeur Go dans le terminal.
//
// Usage : gopit [répertoire]   (par défaut le répertoire courant)
package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/laurentpoirierfr/gopit/internal/app"
	"github.com/laurentpoirierfr/gopit/internal/config"
	"github.com/laurentpoirierfr/gopit/internal/i18n"
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
	if err != nil {
		fmt.Fprintln(os.Stderr, "gopit:", err)
		os.Exit(1)
	}

	if _, err := tea.NewProgram(app.New(proj)).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "gopit:", err)
		os.Exit(1)
	}
}
