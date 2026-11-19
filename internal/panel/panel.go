// Package panel définit le contrat commun à tous les modules du cockpit
// (Accueil, Tests, Diagnostics…). Ajouter un module revient à implémenter
// Panel et à l'enregistrer dans app.New.
package panel

import (
	"slices"

	tea "charm.land/bubbletea/v2"
)

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

// RawInput est implémenté par les panneaux qui veulent toutes les touches,
// raccourcis du cockpit compris (le vrai terminal : vim, htop…) : tant que
// RawInput renvoie true, le cadre leur transmet tout, Ctrl+C, Ctrl+Q et les
// touches F comprises. Le panneau prévoit sa propre touche pour rendre la
// main.
type RawInput interface {
	RawInput() bool
}

// CtrlCTaker est implémenté par les panneaux qui donnent un sens à Ctrl+C
// (copier dans l'éditeur, arrêter la commande du terminal) : tant que
// TakesCtrlC renvoie true, Ctrl+C leur est transmis au lieu de quitter.
// Ctrl+Q quitte toujours.
type CtrlCTaker interface {
	TakesCtrlC() bool
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

// SavedMsg est diffusé à tous les modules quand un fichier est enregistré
// dans l'éditeur intégré (pour relancer une analyse…).
type SavedMsg struct{ Path string }

// Saved renvoie une commande qui diffuse SavedMsg.
func Saved(path string) tea.Cmd {
	return func() tea.Msg { return SavedMsg{Path: path} }
}

// BranchPickerMsg demande au cadre d'ouvrir la liste des branches git.
type BranchPickerMsg struct{}

// ApplyMsg demande de remplacer le contenu du fichier Path (Base, tel qu'il
// était) par Text, une correction : dans l'éditeur intégré, sans
// enregistrer, curseur à la ligne Line. Si le texte a changé depuis, rien
// n'est fait.
type ApplyMsg struct {
	Path, Base, Text string
	Line             int
	Title            string
}

// TextApplier est implémenté par l'éditeur intégré, qui applique les
// corrections (ApplyMsg).
type TextApplier interface {
	ApplyText(msg ApplyMsg) tea.Cmd
}

// GotoMsg demande d'afficher le module de titre Title (titre non traduit,
// celui que renvoie Panel.Title).
type GotoMsg struct{ Title string }

// RepoChangedMsg est diffusé après un changement de branche : les fichiers
// du projet ont pu changer sur le disque.
type RepoChangedMsg struct{}

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
	ActNew       EditAction = "new"
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
	X, Y   int
	Drag   bool
	Shift  bool
	Double bool     // deuxième clic rapide sur la même zone (ouvrir, comme Entrée)
	Zones  []string // zones sous la souris (voir Zone), de la plus précise à la plus large
}

// ─── Débogage ────────────────────────────────────────────────────────────────

// DebugState est l'état de débogage partagé entre l'éditeur (qui pose les
// points d'arrêt et montre la ligne où le programme est arrêté) et le
// module Débogage (qui pilote Delve). Le cadre le crée et le confie aux
// modules qui implémentent DebugUser ; on n'y accède que depuis la boucle
// de Bubble Tea.
type DebugState struct {
	bps      map[string]map[int]bool // chemin absolu → lignes (à partir de 1)
	StopPath string                  // position où le programme est arrêté
	StopLine int                     // (0 : nulle part)
	Current  string                  // fichier affiché dans l'éditeur (cible automatique)
}

// NewDebugState crée un état vide.
func NewDebugState() *DebugState { return &DebugState{bps: map[string]map[int]bool{}} }

// Toggle pose ou retire un point d'arrêt ; il renvoie true s'il est posé.
func (d *DebugState) Toggle(path string, line int) bool {
	if d.bps[path][line] {
		delete(d.bps[path], line)
		if len(d.bps[path]) == 0 {
			delete(d.bps, path)
		}
		return false
	}
	if d.bps[path] == nil {
		d.bps[path] = map[int]bool{}
	}
	d.bps[path][line] = true
	return true
}

// Has indique si la ligne a un point d'arrêt.
func (d *DebugState) Has(path string, line int) bool { return d.bps[path][line] }

// Lines renvoie les lignes des points d'arrêt d'un fichier, dans l'ordre.
func (d *DebugState) Lines(path string) []int {
	var out []int
	for l := range d.bps[path] {
		out = append(out, l)
	}
	slices.Sort(out)
	return out
}

// Files renvoie les fichiers qui ont des points d'arrêt, dans l'ordre.
func (d *DebugState) Files() []string {
	var out []string
	for p := range d.bps {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

// DebugUser est implémenté par les modules qui partagent l'état de débogage.
type DebugUser interface {
	UseDebug(*DebugState)
}

// Debugger est implémenté par le module Débogage : le cadre lui transmet
// les touches F5 à F8, quel que soit le module affiché.
type Debugger interface {
	DebugKey(key string) tea.Cmd
}

// OutputMsg est une ligne écrite par le programme débogué : le cadre
// l'affiche dans la barre d'état quand le module Débogage n'est pas affiché.
type OutputMsg struct {
	Line string
	Err  bool // sortie d'erreur
}

// BreakpointsMsg est diffusé quand les points d'arrêt d'un fichier changent.
type BreakpointsMsg struct{ Path string }

// ShowMsg demande d'afficher une position dans l'éditeur (ligne où le
// programme est arrêté) ; le cadre ne quitte pas le module Débogage s'il
// est affiché.
type ShowMsg struct {
	File string
	Line int
}

// Shower est implémenté par l'éditeur : afficher une position, en lecture.
type Shower interface {
	ShowLine(file string, line int) tea.Cmd
}
