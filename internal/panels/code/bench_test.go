package code

import (
	"os"
	"testing"
)

// La coloration est recalculée à chaque modification du texte : elle doit
// rester bien plus rapide que la frappe.

func BenchmarkHighlightGo(b *testing.B) {
	src, err := os.ReadFile("code.go") // un vrai source de gopit
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		highlightGo(string(src))
	}
}

func BenchmarkHighlightMarkdown(b *testing.B) {
	src, err := os.ReadFile("../../../README.md")
	if err != nil {
		b.Fatal(err)
	}
	color := detectLanguage("README.md", "").color
	for b.Loop() {
		color(string(src))
	}
}
