# gopit

Le cockpit du développeur Go dans le terminal.

gopit ne remplace pas ton éditeur : il l'accompagne. Il rassemble dans une
interface clavier (et souris) ce que les éditeurs font mal — suivre les tests,
lire les diagnostics, explorer les dépendances, consulter la documentation —
et ouvre ton éditeur (`$VISUAL` / `$EDITOR`) quand il faut modifier du code.

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
| `↑` `↓` ou `1`…`5` | choisir un module |
| `Tab` / `Échap` | passer de la navigation au module et inversement |
| `e` | ouvrir l'éditeur sur le projet |
| `q` | quitter |

## Modules

| Module | État |
|---|---|
| Accueil — résumé du module (version Go, paquets, fichiers, git) | ✅ |
| Tests — `go test -json` en direct, relance ciblée, ouverture de l'erreur, relance automatique à l'enregistrement (`w`), couverture par paquet et par fichier (`v`) | ✅ |
| Diagnostics — `go vet`, staticcheck, gopls et gofmt, fusionnés, avec extrait du code | ✅ |
| Modules — dépendances, mises à jour (`go get` après confirmation), `go mod why`, `govulncheck` | ✅ |
| Docs — documentation des paquets du module et de la bibliothèque standard, rendue avec Glamour | ✅ |

## Architecture

```
cmd/gopit/            point d'entrée (options, détection du module)
internal/app/         cadre de l'application : navigation, barres, focus
internal/panel/       interface Panel commune à tous les modules
internal/panels/      un paquet par module (home, tests, diagnostics, modules, docs)
internal/project/     lecture de go.mod
internal/theme/       palette Catppuccin claire / sombre et styles
internal/version/     version injectée par le Makefile

Logique sans interface (testable seule) :
internal/gotest/      go test -json : exécution et état des tests
internal/diag/        go vet, staticcheck, gopls, gofmt : lecture et fusion
internal/gomod/       go list -m, go mod why, go get, govulncheck
internal/godoc/       documentation d'un paquet en Markdown (go/doc)
internal/editor/      ouverture de $VISUAL / $EDITOR à une ligne donnée
```

Ajouter un module : créer `internal/panels/<nom>` qui implémente
`panel.Panel`, puis l'enregistrer dans `app.New`. Un module qui a une saisie
de texte implémente aussi `panel.Capturer` pour recevoir toutes les touches.

## Développement

```sh
make help     # liste des cibles
make check    # formatage + go vet + tests
```
