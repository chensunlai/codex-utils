package history

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

func LoadModelSettings(configPath string) (ModelSettings, error) {
	settings := ModelSettings{Provider: DefaultProvider, Model: DefaultModel}
	raw, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return ModelSettings{}, fmt.Errorf("read config: %w", err)
	}

	var data map[string]any
	if err := toml.Unmarshal(raw, &data); err != nil {
		return ModelSettings{}, fmt.Errorf("parse %s: %w", configPath, err)
	}

	if value := firstString(data,
		[]string{"model_provider"},
		[]string{"modelProvider"},
		[]string{"provider"},
		[]string{"defaults", "model_provider"},
		[]string{"defaults", "provider"},
	); value != "" {
		settings.Provider = value
	}
	if value := firstString(data,
		[]string{"model"},
		[]string{"defaults", "model"},
	); value != "" {
		settings.Model = value
	}
	return settings, nil
}

func firstString(data map[string]any, paths ...[]string) string {
	for _, keys := range paths {
		var current any = data
		for _, key := range keys {
			mapping, ok := current.(map[string]any)
			if !ok {
				current = nil
				break
			}
			current = mapping[key]
		}
		if value, ok := current.(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
