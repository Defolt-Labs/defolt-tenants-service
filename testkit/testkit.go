// Package testkit provides shared test helpers.
package testkit

import (
	"os"
	"path/filepath"
)

// RepoPath returns the absolute path of a file named relative to the module root.
func RepoPath(rel string) string {
	dir, err := os.Getwd()
	if err != nil {
		panic("testkit: getwd: " + err.Error())
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, filepath.FromSlash(rel))
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("testkit: no go.mod above " + dir)
		}
		dir = parent
	}
}
