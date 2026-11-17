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
