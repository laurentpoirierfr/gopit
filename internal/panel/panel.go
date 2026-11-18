// Package panel définit le contrat commun à tous les modules du cockpit
// (Accueil, Tests, Diagnostics…). Ajouter un module revient à implémenter
// Panel et à l'enregistrer dans app.New.
package panel

import tea "charm.land/bubbletea/v2"

// Hint est un raccourci affiché dans la ligne d'aide en bas de l'écran.
type Hint struct {
	Key, Desc string
}

// Panel est un module affiché dans la zone principale.
type Panel interface {
	// Title et Icon apparaissent dans la navigation à gauche.
	Title() string
	Icon() string

	// Init lance le travail initial (chargement asynchrone…).
	Init() tea.Cmd

	// Update reçoit tous les messages non clavier (résultats de commandes,
	// taille de fenêtre…) et les touches quand le panneau a le focus.
	Update(msg tea.Msg) (Panel, tea.Cmd)

	// View dessine le contenu dans une zone de width×height cellules.
	View(width, height int) string

	// Hints renvoie les raccourcis propres au panneau.
	Hints() []Hint
}

// Capturer est implémenté par les panneaux qui ont une saisie de texte
// (recherche…) : tant que Capturing renvoie true, toutes les touches leur
// sont transmises (sauf Ctrl+C et Ctrl+Q), y compris q, e, Tab et les
// chiffres.
type Capturer interface {
	Capturing() bool
}

// Copier est implémenté par les panneaux où Ctrl+C copie au lieu de quitter
// (l'éditeur) : tant que CopiesOnCtrlC renvoie true, Ctrl+C leur est transmis.
// Ctrl+Q quitte toujours.
type Copier interface {
	CopiesOnCtrlC() bool
}

// FlashMsg permet à un panneau d'afficher un message temporaire dans la barre
// d'état.
type FlashMsg struct {
	Text  string
	Error bool
}

// Flash renvoie une commande qui affiche un message dans la barre d'état.
func Flash(text string, isErr bool) tea.Cmd {
	return func() tea.Msg { return FlashMsg{Text: text, Error: isErr} }
}

// LangMsg est envoyé à tous les modules quand la langue de l'interface
// change : ceux qui gardent du texte déjà traduit le recalculent.
type LangMsg struct{}

// Command est une action d'un module, proposée dans la palette (Ctrl+P).
// L'exécuter revient à afficher le module et à lui envoyer la touche Key
// (au format de tea.KeyPressMsg.String : « a », « enter », « f5 »…), ou le
// message Msg s'il est renseigné (Key n'est alors qu'affichée).
type Command struct {
	Title string
	Key   string
	Msg   tea.Msg
}

// Commander est implémenté par les modules qui exposent des commandes à la
// palette.
type Commander interface {
	Commands() []Command
}

// Persister est implémenté par les modules qui ont des réglages à garder
// d'une session à l'autre (enregistrés dans ~/.config/gopit/config.json).
type Persister interface {
	State() map[string]string
	Restore(state map[string]string)
}

// ─── Éditeur intégré ─────────────────────────────────────────────────────────

// OpenMsg demande l'ouverture d'un fichier à une ligne (1 = première ; 0 :
// sans ligne précise). Le cadre l'envoie à l'éditeur intégré, ou à l'éditeur
// externe selon les préférences.
type OpenMsg struct {
	File string
	Line int
}

// Open renvoie une commande qui ouvre file à la ligne line.
func Open(file string, line int) tea.Cmd {
	return func() tea.Msg { return OpenMsg{File: file, Line: line} }
}

// Opener est implémenté par le module qui ouvre les fichiers (éditeur).
type Opener interface {
	OpenFile(file string, line int) tea.Cmd
}

// EditAction est une action du menu Édition.
type EditAction string

const (
	ActUndo      EditAction = "undo"
	ActRedo      EditAction = "redo"
	ActCut       EditAction = "cut"
	ActCopy      EditAction = "copy"
	ActPaste     EditAction = "paste"
	ActSelectAll EditAction = "selectall"
	ActFind      EditAction = "find"
	ActFindNext  EditAction = "findnext"
	ActReplace   EditAction = "replace"
	ActGoto      EditAction = "goto"
	ActComment   EditAction = "comment"
	ActFormat    EditAction = "format"
	ActOpen      EditAction = "open"
	ActSave      EditAction = "save"
	ActClose     EditAction = "close"
	// Avec gopls : aller à la définition, revenir, documentation du symbole.
	ActDefinition EditAction = "definition"
	ActBack       EditAction = "back"
	ActDoc        EditAction = "doc"
)

// EditMsg transmet une action du menu Édition (ou de la palette) au module.
type EditMsg struct{ Action EditAction }

// Editor est implémenté par les modules qui acceptent les actions du menu
// Édition ; CanEdit indique si elles sont disponibles (fichier ouvert…).
type Editor interface {
	CanEdit() bool
}

// Guard est implémenté par les modules qui peuvent avoir un travail non
// enregistré : Unsaved renvoie ce qui serait perdu en quittant.
type Guard interface {
	Unsaved() []string
}

// ClickMsg est un clic (ou un glissement, bouton gauche enfoncé) dans la
// zone du module, en coordonnées relatives à cette zone (celle de View).
type ClickMsg struct {
	X, Y  int
	Drag  bool
	Shift bool
}
