package editor

import (
	"reflect"
	"testing"
)

func TestArgs(t *testing.T) {
	cases := []struct {
		ed   string
		file string
		line int
		want []string
	}{
		{"nvim", "a.go", 12, []string{"+12", "a.go"}},
		{"/usr/bin/micro", "a.go", 3, []string{"+3", "a.go"}},
		{"code --wait", "a.go", 7, []string{"-g", "a.go:7"}},
		{"hx", "a.go", 5, []string{"a.go:5"}},
		{"nvim", "a.go", 0, []string{"a.go"}},
		{"nvim", "", 0, []string{"/projet"}},
	}
	for _, c := range cases {
		if got := Args(c.ed, "/projet", c.file, c.line); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Args(%q, %q, %d) = %v, attendu %v", c.ed, c.file, c.line, got, c.want)
		}
	}
}
