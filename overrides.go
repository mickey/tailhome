package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Override struct {
	Label       string `yaml:"label"`
	Description string `yaml:"description"`
	Icon        string `yaml:"icon"`
	Group       string `yaml:"group"`
	Order       int    `yaml:"order"`
	Hidden      bool   `yaml:"hidden"`
	URL         string `yaml:"url"`
	Web         *bool  `yaml:"web"`
	Copy        string `yaml:"copy"`
}

// LoadOverrides returns overrides keyed by Service name without "svc:".
// A missing file means no overrides; unknown keys are errors.
func LoadOverrides(path string) (map[string]Override, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var raw map[string]Override
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := make(map[string]Override, len(raw))
	for key, o := range raw {
		name := strings.TrimPrefix(key, "svc:")
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("%s: %s is listed twice", path, name)
		}
		out[name] = o
	}
	return out, nil
}
