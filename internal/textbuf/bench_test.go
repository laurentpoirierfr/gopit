package textbuf

import (
	"fmt"
	"strings"
	"testing"
)

// bigText est un texte de 5000 lignes, comme un gros fichier source.
func bigText() string {
	var b strings.Builder
	for i := range 5000 {
		fmt.Fprintf(&b, "\tvalue%d := compute(%d, \"texte\") // ligne %d\n", i, i, i)
	}
	return b.String()
}

// BenchmarkTyping mesure la frappe d'un caractère au milieu d'un gros fichier.
func BenchmarkTyping(b *testing.B) {
	buf := New(bigText())
	buf.SetCursor(Pos{Line: 2500, Col: 10}, false)
	for b.Loop() {
		buf.Insert("x")
	}
}

// BenchmarkFind mesure une recherche sans résultat (tout le fichier parcouru).
func BenchmarkFind(b *testing.B) {
	buf := New(bigText())
	for b.Loop() {
		buf.Find("introuvable", true)
	}
}

// BenchmarkReplaceAll mesure un remplacement dans tout le fichier.
func BenchmarkReplaceAll(b *testing.B) {
	text := bigText()
	for b.Loop() {
		New(text).ReplaceAll("compute", "calcule")
	}
}
