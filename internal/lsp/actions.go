package lsp

// Corrections proposées par le serveur (textDocument/codeAction) et
// application de leurs modifications à un texte.

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
)

// TextEdit remplace le texte entre Start et End d'un fichier.
type TextEdit struct {
	Path       string
	Start, End Pos
	NewText    string
}

// CodeAction est une correction (ou une transformation) proposée par le
// serveur, avec ses modifications.
type CodeAction struct {
	Title string
	Kind  string // quickfix, refactor.rewrite…
	Edits []TextEdit
}

type wireEdit struct {
	Range   wireRange `json:"range"`
	NewText string    `json:"newText"`
}

type wireAction struct {
	Title string `json:"title"`
	Kind  string `json:"kind"`
	Edit  *struct {
		Changes         map[string][]wireEdit `json:"changes"`
		DocumentChanges []struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			Edits []wireEdit `json:"edits"`
		} `json:"documentChanges"`
	} `json:"edit"`
	Data json.RawMessage `json:"data"`
}

// CodeActions demande les actions du serveur pour la zone [start, end) de
// path, en lui rappelant ses diagnostics de cette zone (diags, tels qu'il
// les a publiés) ; only filtre les sortes (ex. « quickfix »). Les actions
// sans modification (simples commandes) sont écartées ; celles dont les
// modifications sont calculées à la demande sont résolues.
func (c *Client) CodeActions(ctx context.Context, path string, start, end Pos, diags []Diagnostic, only ...string) ([]CodeAction, error) {
	ds := make([]json.RawMessage, 0, len(diags))
	for _, d := range diags {
		if d.raw != nil {
			ds = append(ds, d.raw)
		}
	}
	ctxParams := map[string]any{"diagnostics": ds}
	if len(only) > 0 {
		ctxParams["only"] = only
	}
	params := map[string]any{
		"textDocument": map[string]string{"uri": fileURI(path)},
		"range":        wireRange{Start: c.toWire(path, start), End: c.toWire(path, end)},
		"context":      ctxParams,
	}
	var raws []json.RawMessage
	if err := c.call(ctx, "textDocument/codeAction", params, &raws); err != nil {
		return nil, err
	}
	var out []CodeAction
	for _, raw := range raws {
		var a wireAction
		if json.Unmarshal(raw, &a) != nil || a.Title == "" {
			continue // commande seule
		}
		if len(only) > 0 && !slices.ContainsFunc(only, func(k string) bool { return a.Kind == k || strings.HasPrefix(a.Kind, k+".") }) {
			continue
		}
		if a.Edit == nil && a.Data != nil { // modifications à demander
			var r wireAction
			if err := c.call(ctx, "codeAction/resolve", raw, &r); err == nil {
				a.Edit = r.Edit
			}
		}
		if a.Edit == nil {
			continue
		}
		act := CodeAction{Title: a.Title, Kind: a.Kind}
		add := func(uri string, edits []wireEdit) {
			p, err := uriPath(uri)
			if err != nil {
				return
			}
			for _, e := range edits {
				act.Edits = append(act.Edits, TextEdit{Path: p, Start: c.fromWire(p, e.Range.Start),
					End: c.fromWire(p, e.Range.End), NewText: e.NewText})
			}
		}
		for _, dc := range a.Edit.DocumentChanges {
			add(dc.TextDocument.URI, dc.Edits)
		}
		for uri, edits := range a.Edit.Changes {
			add(uri, edits)
		}
		if len(act.Edits) > 0 {
			out = append(out, act)
		}
	}
	return out, nil
}

// Apply applique à text (le contenu de path) les modifications qui le
// concernent ; les autres sont ignorées. Les modifications ne doivent pas
// se chevaucher (garantie du protocole).
func Apply(text, path string, edits []TextEdit) string {
	lines := strings.Split(text, "\n")
	offset := func(p Pos) int {
		n := 0
		for l := 0; l < p.Line && l < len(lines); l++ {
			n += len(lines[l]) + 1
		}
		if p.Line >= len(lines) {
			return len(text)
		}
		line := lines[p.Line]
		col := 0
		for i := range line {
			if col == p.Col {
				return n + i
			}
			col++
		}
		return n + len(line)
	}
	type span struct {
		s, e int
		text string
	}
	var spans []span
	for _, e := range edits {
		if e.Path == path {
			spans = append(spans, span{offset(e.Start), offset(e.End), e.NewText})
		}
	}
	// De la fin vers le début : les positions restent valables.
	slices.SortStableFunc(spans, func(a, b span) int { return b.s - a.s })
	for _, sp := range spans {
		text = text[:sp.s] + sp.text + text[sp.e:]
	}
	return text
}
