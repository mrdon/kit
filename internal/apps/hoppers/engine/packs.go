package engine

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// Built-in packs are whole nights (rules plus a cup of levels) shipped as
// data: engine/levels/*.yaml, embedded in the binary, the same files as the
// repo's levels/ examples. A host picks one by name instead of a file path.
//
//go:embed levels/*.yaml
var packFiles embed.FS

// BuiltinPacks is the name of every built-in pack, sorted ("default",
// "chutes-and-ladders", ...).
func BuiltinPacks() []string {
	entries, err := fs.ReadDir(packFiles, "levels")
	if err != nil {
		return nil // can't happen: the directory is embedded
	}
	var names []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".yaml"); ok {
			names = append(names, n)
		}
	}
	slices.Sort(names)
	return names
}

// BuiltinPackData is a built-in pack's file as shipped (YAML).
func BuiltinPackData(name string) ([]byte, error) {
	if !slices.Contains(BuiltinPacks(), name) {
		return nil, fmt.Errorf("pack %q: no such pack (have %v)", name, BuiltinPacks())
	}
	data, err := packFiles.ReadFile(path.Join("levels", name+".yaml"))
	if err != nil {
		return nil, fmt.Errorf("pack %q: %w", name, err)
	}
	return data, nil
}

// BuiltinPack is a built-in pack's config, parsed and validated.
func BuiltinPack(name string) (GameConfig, error) {
	data, err := BuiltinPackData(name)
	if err != nil {
		return GameConfig{}, err
	}
	cfg, err := ParseConfig(data)
	if err != nil {
		return GameConfig{}, fmt.Errorf("pack %q: %w", name, err)
	}
	return cfg, nil
}
