package main

import (
	"errors"
	"path/filepath"
)

// A successful package-manager exit and --version alone do not establish that
// the launch path still belongs to the package that the user approved.
func verifyPackageUpdateOwnership(rt packageUpdateRuntime, previous packageInstallation, newBinary, target string) error {
	realBinary, err := filepath.EvalSymlinks(newBinary)
	if err != nil {
		return errors.New("Cannot locate the newly installed executable.")
	}
	installedVersion, err := packageUpdateProbe(rt, newBinary, "--version")
	if err != nil || !packageUpdateAtLeast(installedVersion, target) {
		return errors.New("Cannot verify the newly installed version.")
	}
	rt.binary, rt.current = realBinary, installedVersion
	updated, err := detectPackageInstallation(rt)
	if err != nil || updated.Method != previous.Method || updated.Package != previous.Package || updated.Manager != previous.Manager || updated.Digest == "" || !packageUpdateAtLeast(updated.Version, target) {
		return errors.New("Cannot verify ownership of the newly installed application.")
	}
	return nil
}
