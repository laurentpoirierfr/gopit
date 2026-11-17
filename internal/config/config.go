// Package config enregistre les préférences de gopit dans
// ~/.config/gopit/config.json (ou $XDG_CONFIG_HOME/gopit/config.json).
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Config regroupe les préférences.
type Config struct {
	// LastModule est le titre du dernier module affiché.
	LastModule string `json:"last_module,omitempty"`
	// Sidebar affiche la barre latérale des modules.
	Sidebar bool `json:"sidebar,omitempty"`
	// HideHints masque la ligne des raccourcis en bas de l'écran.
	HideHints bool `json:"hide_hints,omitempty"`
	// Panels contient les réglages de chaque module, par titre.
	Panels map[string]map[string]string `json:"panels,omitempty"`
}

// Path renvoie l'emplacement du fichier de configuration.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "gopit", "config.json"), nil
}

// Load lit la configuration ; un fichier absent donne une configuration vide.
func Load() (*Config, error) {
	c := &Config{Panels: map[string]map[string]string{}}
	p, err := Path()
	if err != nil {
		return c, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return &Config{Panels: map[string]map[string]string{}}, err
	}
	if c.Panels == nil {
		c.Panels = map[string]map[string]string{}
	}
	return c, nil
}

// Save écrit la configuration (dans un fichier temporaire renommé ensuite,
// pour ne jamais laisser un fichier à moitié écrit).
func (c *Config) Save() error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
