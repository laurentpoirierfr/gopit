# Journal des modifications

Toutes les évolutions notables de gopit sont consignées ici.

Le format suit [Keep a Changelog](https://keepachangelog.com/fr/1.1.0/) et
gopit respecte le [versionnage sémantique](https://semver.org/lang/fr/).
Chaque version publiée reprend sa section comme notes de version sur GitHub :
ajouter ses changements sous « Non publié » au fil de l'eau, puis renommer
cette section au moment de créer le tag (`make release-notes TAG=vX.Y.Z`
montre ce qui sera publié).

## [Non publié]

### Ajouté

- Images dans l'éditeur (PNG, JPEG, GIF, BMP, TIFF, WebP) : onglet en lecture
  seule, en haute définition dans Kitty et Ghostty, sinon en caractères
  colorés (chafa s'il est installé) ; rendu par
  [imgview](https://github.com/laurentpoirierfr/imgview).
- `make screenshots` : captures d'écran de tous les modules, produites en
  pilotant gopit sur son propre dépôt (`assets/screenshots/`).
- Benchmarks de l'éditeur (frappe, recherche, remplacement, coloration).

### Modifié

- README : nouvelle image principale (l'éditeur) et galerie de captures de
  tous les modules, en français, anglais et espagnol.

## [0.3.0] - 2026-11-18

### Ajouté

- **Éditeur intégré** (module 2) : arborescence du projet, plusieurs fichiers
  ouverts en onglets, coloration syntaxique, indentation automatique, gofmt à
  l'enregistrement, recherche et remplacement (`Ctrl+F`, `Ctrl+H`), sélection
  à la souris, annuler / rétablir, couper / copier / coller (`Ctrl+X`,
  `Ctrl+C`, `Ctrl+V`), aller à la ligne, commenter, création de fichier
  (`Ctrl+N`).
- **gopls dans l'éditeur** : erreurs et avertissements soulignés pendant la
  frappe, complétion (`Ctrl+Espace`, ou après un `.`), aller à la définition
  (`F12`, retour `Ctrl+T`) et documentation du symbole (`Ctrl+K`), y compris
  sur du texte non enregistré.
- Coloration des fichiers non Go (Markdown, YAML, Makefile, Dockerfile, JSON,
  SQL, scripts shell… grâce à chroma) ; `go.mod` et `go.sum` ont la leur.
- L'arborescence suit le disque : fichiers et dossiers créés, supprimés ou
  renommés ailleurs apparaissent d'eux-mêmes, un fichier ouvert non modifié est
  relu s'il change.
- **Module Terminal** : un vrai shell (`$SHELL`) dans un pseudo-terminal, avec
  les programmes interactifs (vim, htop, `git rebase -i`…) ; `Ctrl+]` rend la
  main à gopit (historique de l'écran, ouverture des `fichier.go:ligne`).
- **Module Débogage** avec Delve : points d'arrêt dans l'éditeur (`F9`),
  lancer / continuer (`F5`), pas à pas (`F6`, `F7`, `F8`), arrêt (`Maj+F5`),
  pile d'appels, variables dépliables, sortie du programme en plein écran
  (`s`) et dans la barre d'état ; cible automatique (programme ou tests du
  fichier affiché, sinon le programme du module).
- **Git** : changement de branche et création de branche (clic sur la branche
  de la barre d'état, ou `b` dans le module Git) ; la barre d'état montre la
  branche courante, les modifications en cours et les commits à pousser ou à
  récupérer.
- **Interface en français, anglais et espagnol** : langue du système, choix
  dans le menu Affichage (retenu dans les préférences) ou `gopit -lang en` ;
  README en anglais et en espagnol.
- Barre de menus (`F10`, `Alt` + lettre soulignée, propre à chaque langue) et
  barre latérale des modules masquable.
- Lancé hors de tout module Go, gopit propose d'en créer un (`go mod init`),
  avec un chemin déduit du dépôt git ou du compte GitHub.
- Diagnostics relancés automatiquement après chaque enregistrement dans
  l'éditeur et après un changement de branche.

### Modifié

- Les modules suivent l'ordre d'usage : Accueil, Éditeur, Tests, Diagnostics,
  Débogage, Git, Terminal, Docs, Modules, Benchmarks (`0` pour le dixième).
- `F5` à `F8` sont réservées au débogage dans tous les modules ; les autres
  modules relancent avec `a` ou `r`.
- `Ctrl+Q` quitte gopit partout ; `Ctrl+C` copie dans l'éditeur, va au shell
  dans le terminal et quitte ailleurs.
- Le module Terminal n'est plus un lanceur de commandes mais un vrai shell.

### Corrigé

- Accès concurrents dans le client gopls (encodage des colonnes) et dans le
  terminal (fermeture de l'émulateur, lecture de l'historique).

## [0.2.0] - 2026-11-17

### Ajouté

- Tests : relance automatique à l'enregistrement des fichiers (`w`) et
  couverture de code par paquet et par fichier (`v`).
- **Module Benchmarks** : `go test -bench`, historique des exécutions,
  comparaison avec une référence (écart et bruit), évolution en sparkline,
  durée de mesure (`-benchtime`), profils CPU et mémoire (pprof) avec le code
  source annoté.
- **Module Git** : état du dépôt, diff coloré, commits récents ; indexer,
  désindexer, commiter.
- Palette de commandes (`Ctrl+P`) et préférences gardées d'une session à
  l'autre (`~/.config/gopit/config.json`).

### Modifié

- Git : ascenseur et retour à la ligne dans le détail.

## [0.1.0] - 2026-11-17

### Ajouté

- Première version : cockpit du développeur Go dans le terminal, avec les
  modules Accueil (résumé du module), Tests (`go test -json` en direct,
  relance ciblée, ouverture de l'erreur), Diagnostics (`go vet`, staticcheck,
  gopls et gofmt fusionnés), Modules (dépendances, mises à jour,
  `go mod why`, `govulncheck`) et Docs (documentation des paquets).
- Publication avec GoReleaser et GitHub Actions.

[Non publié]: https://github.com/laurentpoirierfr/gopit/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/laurentpoirierfr/gopit/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/laurentpoirierfr/gopit/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/laurentpoirierfr/gopit/releases/tag/v0.1.0
