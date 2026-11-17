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
	"github.com/laurentpoirierfr/gopit/internal/project"
	"github.com/laurentpoirierfr/gopit/internal/version"
)

func main() {
	showVersion := flag.Bool("version", false, "affiche la version et quitte")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage : gopit [options] [répertoire]\n\n")
		fmt.Fprintf(os.Stderr, "Ouvre le cockpit sur le module Go qui contient le répertoire\n(par défaut le répertoire courant).\n\nOptions :\n")
		flag.PrintDefaults()
	}
	flag.Parse()

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
