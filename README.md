# gopit

Le cockpit du développeur Go dans le terminal.

gopit rassemble dans une interface clavier (et souris) ce que les éditeurs
font mal — suivre les tests, lire les diagnostics, explorer les dépendances,
consulter la documentation. Pour modifier du code, il a son propre éditeur
intégré, pensé pour Go ; tu peux aussi garder ton éditeur (`$VISUAL` /
`$EDITOR`) : menu Fichier → « Ouvrir les fichiers dans l'éditeur externe ».

Construit avec la pile [Charm](https://charm.land/) : Bubble Tea v2,
Lip Gloss v2.

![gopit : le module Tests en cours d'exécution sur gopit lui-même](assets/screenshot.png)

## Utilisation

```sh
make build
./bin/gopit ~/Projects/mon-module   # ou : make run ARGS=~/Projects/mon-module
```

gopit remonte depuis le répertoire donné (par défaut le répertoire courant)
jusqu'au premier `go.mod`.

| Touche | Action |
|---|---|
| `↑` `↓` ou `1`…`8` | choisir un module |
| `Ctrl+P` | palette de commandes : toutes les actions de tous les modules |
| `Ctrl+O` | ouvrir un fichier du projet dans l'éditeur intégré |
| `Tab` / `Échap` | passer de la navigation au module et inversement |
| `e` | ouvrir l'éditeur externe sur le projet |
| `q` / `Ctrl+Q` | quitter (confirmation s'il reste des fichiers non enregistrés) ; `Ctrl+C` quitte aussi, sauf dans l'éditeur où il copie |

## Préférences

Les réglages (dernier module affiché, couverture, cache, filtres,
répétitions des benchmarks…) sont enregistrés en quittant dans
`~/.config/gopit/config.json` et relus au démarrage. L'historique des
benchmarks est rangé dans `~/.cache/gopit/bench/`.

## Modules

| Module | État |
|---|---|
| Accueil — résumé du module (version Go, paquets, fichiers, git) | ✅ |
| Tests — `go test -json` en direct, relance ciblée, ouverture de l'erreur, relance automatique à l'enregistrement (`w`), couverture par paquet et par fichier (`v`) | ✅ |
| Diagnostics — `go vet`, staticcheck, gopls et gofmt, fusionnés, avec extrait du code | ✅ |
| Modules — dépendances, mises à jour (`go get` après confirmation), `go mod why`, `govulncheck` | ✅ |
| Docs — documentation des paquets du module et de la bibliothèque standard, rendue avec Glamour | ✅ |
| Benchmarks — `go test -bench`, historique des exécutions, comparaison avec une référence (Δ % et bruit), évolution en sparkline, profils CPU et mémoire (`p`, `m`) avec code source annoté | ✅ |
| Git — état du dépôt, diff coloré, commits récents ; indexer (`s`, `A`), désindexer (`u`), commiter (`c`) — sans action destructive ni push | ✅ |
| Éditeur — arborescence du projet, plusieurs fichiers ouverts, coloration Go, indentation automatique, gofmt à l'enregistrement, recherche, sélection à la souris, annuler/rétablir | ✅ |

### Éditeur intégré

Ouvrir un fichier depuis Tests, Diagnostics, Docs, Benchmarks ou Git
(`Entrée` / `o`) l'affiche dans l'éditeur, à la bonne ligne. Deux modes :
**lecture** (les touches du cockpit restent actives) et **édition** (`Entrée`
ou un clic ; `Échap` pour en sortir).

| Touche | Action |
|---|---|
| `Ctrl+S` | enregistrer (formaté avec gofmt pour un fichier Go) |
| `Ctrl+O` | ouvrir un fichier (recherche par nom) |
| `Ctrl+W` ou `Ctrl+F4` | fermer le fichier (aussi : `w` en lecture, la croix `×` de l'onglet) |
| `Ctrl+B` | afficher / masquer l'arborescence |
| `Ctrl+E` | passer du texte à l'arborescence et inversement |
| `Ctrl+PgDn` / `Ctrl+PgUp` | fichier suivant / précédent |
| `Ctrl+Z` / `Ctrl+Y` | annuler / rétablir |
| `Ctrl+X` / `Ctrl+C` / `Ctrl+V` | couper / copier (la ligne sans sélection) / coller |
| `Ctrl+F`, `F3`, `Maj+F3` | rechercher, occurrence suivante, précédente |
| `Ctrl+G` | aller à la ligne |
| `Tab` / `Maj+Tab` | indenter / désindenter (bloc sélectionné) |
| `Ctrl+/` | commenter / décommenter |
| `Alt+Maj+F` | formater sans enregistrer |

Dans l'arborescence : `↑` `↓` pour naviguer, `→` / `←` pour déplier ou
replier un dossier, `Entrée` pour ouvrir, `r` pour relire le disque. Les
fichiers ouverts, les dossiers dépliés et l'affichage de l'arborescence sont
retrouvés au démarrage suivant.

## Architecture

```
cmd/gopit/            point d'entrée (options, détection du module)
internal/app/         cadre de l'application : navigation, barres, focus
internal/panel/       interface Panel commune à tous les modules
internal/panels/      un paquet par module (home, tests, diagnostics, modules, docs, benchmarks, git, code)
internal/project/     lecture de go.mod
internal/theme/       palette Catppuccin claire / sombre et styles
internal/version/     version injectée par le Makefile

Logique sans interface (testable seule) :
internal/gotest/      go test -json : exécution et état des tests
internal/diag/        go vet, staticcheck, gopls, gofmt : lecture et fusion
internal/gomod/       go list -m, go mod why, go get, govulncheck
internal/godoc/       documentation d'un paquet en Markdown (go/doc)
internal/bench/       go test -bench : lecture, comparaison, historique
internal/prof/        profils pprof : coût par fonction et par ligne
internal/gitx/        git status / diff / log / add / restore / commit
internal/textbuf/     tampon de l'éditeur : curseur, sélection, annuler/rétablir
internal/config/      préférences (~/.config/gopit/config.json)
internal/cover/       profils de couverture
internal/watch/       surveillance des fichiers .go
internal/editor/      ouverture de $VISUAL / $EDITOR à une ligne donnée
```

Ajouter un module : créer `internal/panels/<nom>` qui implémente
`panel.Panel`, puis l'enregistrer dans `app.New`. Un module qui a une saisie
de texte implémente aussi `panel.Capturer` pour recevoir toutes les touches,
`panel.Commander` pour apparaître dans la palette (Ctrl+P) et
`panel.Persister` pour garder ses réglages d'une session à l'autre.
Pour ouvrir un fichier, un module renvoie `panel.Open(fichier, ligne)` : le
cadre l'envoie à l'éditeur intégré ou externe selon les préférences.

## Développement

```sh
make help     # liste des cibles
make check    # formatage + go vet + tests
```
