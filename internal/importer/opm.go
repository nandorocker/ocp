// Package importer adds support for importing from OPM profile directories.
package importer

import (
	"os"
	"path/filepath"
	"slices"
)

const opmConfigDir = ".config/opm/profiles"

// DefaultOPMProfileDir returns the default OPM profiles directory.
func DefaultOPMProfileDir() string {
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, opmConfigDir)
}

// HasOPMProfiles reports whether the default OPM profiles directory exists and contains profiles.
func HasOPMProfiles() bool {
	dir := DefaultOPMProfileDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		opcJSON := filepath.Join(dir, e.Name(), "opencode.json")
		if _, err := os.Stat(opcJSON); err == nil {
			return true
		}
	}
	return false
}

// ListOPMProfiles returns a sorted list of available OPM profile names.
func ListOPMProfiles(profileDir string) ([]string, error) {
	entries, err := os.ReadDir(profileDir)
	if err != nil {
		return nil, err
	}
	var profiles []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		opcJSON := filepath.Join(profileDir, e.Name(), "opencode.json")
		if _, err := os.Stat(opcJSON); err == nil {
			profiles = append(profiles, e.Name())
		}
	}
	slices.Sort(profiles)
	return profiles, nil
}

// ImportOPM delegates to Import after locating the selected OPM profile.
func ImportOPM(source string, force bool, selectFn func(profiles []string) (string, bool)) error {
	profileDir := DefaultOPMProfileDir()
	profiles, err := ListOPMProfiles(profileDir)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		return nil // No-op: nothing to import
	}
	name, ok := selectFn(profiles)
	if !ok {
		return nil
	}
	inputPath := filepath.Join(profileDir, name)
	return Import(inputPath, source, force)
}
