package main

import "path/filepath"

// Each server profile owns its generated CLI sessions too. Two services or a
// desktop instance must not rewrite each other's connection/model settings.
func (a *app) setHeadlessProfilePaths() error {
	root := filepath.Join(a.dir, "profiles")
	if err := secureHeadlessProfile(root); err != nil {
		return err
	}
	a.codexCLIProfileDir = filepath.Join(root, "codex")
	a.claudeProfileDir = filepath.Join(root, "claude")
	a.ompProfileDir = filepath.Join(root, "omp")
	a.openCodeProfileDir = filepath.Join(root, "opencode")
	return nil
}
