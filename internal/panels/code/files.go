package code

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxFiles    = 5000    // fichiers proposés à l'ouverture
	maxFileSize = 4 << 20 // taille maximale d'un fichier ouvert
)

// textExt et textNames désignent les fichiers proposés à l'ouverture.
var textExt = map[string]bool{
	".go": true, ".mod": true, ".sum": true, ".work": true, ".md": true, ".txt": true,
	".yaml": true, ".yml": true, ".json": true, ".toml": true, ".sh": true,
	".tmpl": true, ".gotmpl": true, ".html": true, ".css": true, ".js": true, ".ts": true,
	".sql": true, ".proto": true, ".xml": true, ".csv": true, ".env": true, ".ini": true,
}

var textNames = map[string]bool{
	"Makefile": true, "Dockerfile": true, "LICENSE": true, "README": true,
	".gitignore": true, ".goreleaser.yaml": true, ".golangci.yml": true, ".actrc": true,
}

// skipDir indique les répertoires à ne pas parcourir.
func skipDir(name string) bool {
	switch name {
	case "vendor", "node_modules", "bin", "dist":
		return true
	}
	return strings.HasPrefix(name, ".") && name != "." && name != ".github"
}

// listFiles renvoie les fichiers texte du projet, en chemins relatifs triés.
func listFiles(root string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // répertoire illisible : on continue
		}
		if d.IsDir() {
			if p != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !textExt[filepath.Ext(p)] && !textNames[d.Name()] {
			return nil
		}
		if rel, err := filepath.Rel(root, p); err == nil {
			files = append(files, filepath.ToSlash(rel))
		}
		if len(files) >= maxFiles {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(files)
	return files
}

// filterFiles garde les fichiers qui correspondent à query (casse ignorée),
// les meilleurs d'abord : nom du fichier qui contient la saisie, puis chemin
// qui la contient, puis lettres dans l'ordre (« pcode » → panels/code).
func filterFiles(files []string, query string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return files
	}
	type scored struct {
		path  string
		score int
	}
	var out []scored
	for _, f := range files {
		lf := strings.ToLower(f)
		switch {
		case strings.Contains(strings.ToLower(filepath.Base(f)), q):
			out = append(out, scored{f, 0})
		case strings.Contains(lf, q):
			out = append(out, scored{f, 1})
		case subsequence(lf, q):
			out = append(out, scored{f, 2})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score < out[j].score
		}
		return len(out[i].path) < len(out[j].path)
	})
	res := make([]string, len(out))
	for i, s := range out {
		res[i] = s.path
	}
	return res
}

func subsequence(s, q string) bool {
	for _, r := range q {
		i := strings.IndexRune(s, r)
		if i < 0 {
			return false
		}
		s = s[i+len(string(r)):]
	}
	return true
}

// readText lit un fichier texte, en refusant les fichiers trop gros ou
// binaires.
func readText(path string) (string, fs.FileMode, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	if st.IsDir() {
		return "", 0, fmt.Errorf("%s est un répertoire", filepath.Base(path))
	}
	if st.Size() > maxFileSize {
		return "", 0, fmt.Errorf("%s : fichier trop gros (%d Mo)", filepath.Base(path), st.Size()>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	if bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0 {
		return "", 0, errors.New(filepath.Base(path) + " : fichier binaire")
	}
	return string(data), st.Mode().Perm(), nil
}
