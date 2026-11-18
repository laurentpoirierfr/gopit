// Package i18n traduit l'interface de gopit. Le français est la langue
// source : chaque message s'écrit en français dans le code, entouré de T,
// et les catalogues en.go et es.go donnent sa traduction. Un message absent
// d'un catalogue s'affiche en français.
//
//	i18n.T("Quitter")                         // texte affiché
//	fmt.Sprintf(i18n.T("%d fichiers"), n)     // les verbes de format restent
//	func (m *Model) Title() string { return i18n.N("Tests") } // traduit à l'affichage
//
// La langue peut changer pendant l'exécution : T se lit au moment
// d'afficher, jamais dans une variable de paquet.
package i18n

import (
	"os"
	"strings"
	"sync/atomic"
)

// Lang est un code de langue (« fr », « en », « es »).
type Lang string

const (
	FR Lang = "fr"
	EN Lang = "en"
	ES Lang = "es"
)

// Langs liste les langues disponibles, dans l'ordre des menus.
var Langs = []Lang{FR, EN, ES}

// Name renvoie le nom de la langue, dans cette langue.
func (l Lang) Name() string {
	switch l {
	case EN:
		return "English"
	case ES:
		return "Español"
	}
	return "Français"
}

var catalogs = map[Lang]map[string]string{EN: en, ES: es}

// cur est lu par les commandes lancées en arrière-plan : accès atomique.
var cur atomic.Value

func init() { cur.Store(FR) }

// Set choisit la langue de l'interface ; une langue inconnue donne le
// français.
func Set(l Lang) {
	if _, ok := catalogs[l]; !ok {
		l = FR
	}
	cur.Store(l)
}

// Current renvoie la langue de l'interface.
func Current() Lang { return cur.Load().(Lang) }

// Parse reconnaît une langue d'après un code (« en », « es_ES.UTF-8 »,
// « fr-CA »…).
func Parse(s string) (Lang, bool) {
	s = strings.ToLower(s)
	for _, l := range Langs {
		if s == string(l) || strings.HasPrefix(s, string(l)+"_") || strings.HasPrefix(s, string(l)+"-") || strings.HasPrefix(s, string(l)+".") {
			return l, true
		}
	}
	return "", false
}

// Detect devine la langue du système (LC_ALL, LC_MESSAGES, LANG) ; anglais
// si elle n'est pas disponible.
func Detect() Lang {
	for _, v := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if s := os.Getenv(v); s != "" {
			if l, ok := Parse(s); ok {
				return l
			}
			return EN
		}
	}
	return EN
}

// T traduit un message écrit en français.
func T(fr string) string {
	if t, ok := catalogs[Current()][fr]; ok {
		return t
	}
	return fr
}

// N marque un message à traduire plus tard (à l'affichage, avec T) et le
// renvoie tel quel : titre de module servant aussi de clé, libellé rangé
// dans une variable de paquet…
func N(fr string) string { return fr }

// Error renvoie une erreur dont le message est traduit à chaque lecture :
// pour les erreurs sentinelles déclarées au niveau du paquet.
func Error(fr string) error { return msgError(fr) }

type msgError string

func (e msgError) Error() string { return T(string(e)) }

// Key traduit le libellé d'une touche ou d'une combinaison (« Entrée »,
// « Maj+F3 / ↑ », « PgUp/PgDn Maj+↑↓ molette »).
func Key(k string) string {
	r := keyWords[Current()]
	if r == nil {
		return k
	}
	return r.Replace(k)
}

var keyWords = map[Lang]*strings.Replacer{
	EN: strings.NewReplacer("Entrée", "Enter", "Échap", "Esc", "Maj+", "Shift+", "molette", "wheel", "Suppr", "Del"),
	ES: strings.NewReplacer("Entrée", "Intro", "Échap", "Esc", "Maj+", "Mayús+", "molette", "rueda", "Suppr", "Supr"),
}
