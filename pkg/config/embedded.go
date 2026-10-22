package config

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed config_defaults.yaml
var embeddedDefaultsYAML []byte

// EmbeddedDefaults returns a Config populated from the compiled-in defaults.
func EmbeddedDefaults() *Config {
	c := &Config{
		System:     &SystemConfig{},
		Workspaces: make(map[string]*WorkspaceConfig),
	}
	if err := yaml.Unmarshal(embeddedDefaultsYAML, c); err != nil {
		panic(fmt.Sprintf("failed to unmarshal embedded defaults: %v", err))
	}
	return c
}
