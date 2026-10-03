package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zalando/go-keyring"
)

// Server profiles deliberately do not use a desktop keychain. Credentials in
// this opt-in profile are private files, not encrypted storage; their access
// protection is the operating system's per-user filesystem permissions.
type headlessVault struct{ dir string }

var headlessVaultSlot = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

func headlessConfigDir(input string) (string, error) {
	if input == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", errors.New("Cannot locate the headless configuration directory.")
		}
		input = filepath.Join(base, "kilo-proxy-headless")
	}
	if strings.ContainsAny(input, "\x00\r\n") {
		return "", errors.New("Use a valid configuration directory path.")
	}
	return filepath.Abs(input)
}

func secureHeadlessProfile(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return errors.New("Use an absolute headless profile directory.")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("Cannot create the private headless profile directory.")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || !headlessFileOwned(info) {
		return errors.New("The headless profile must be a private directory owned by the current user (chmod 700), without a symlink.")
	}
	return nil
}

func newHeadlessVault(dir string) (credentialVault, error) {
	if err := secureHeadlessProfile(dir); err != nil {
		return nil, err
	}
	secrets := filepath.Join(dir, "headless-secrets")
	if err := secureHeadlessProfile(secrets); err != nil {
		return nil, err
	}
	return headlessVault{dir: secrets}, nil
}

func (v headlessVault) path(slot string) (string, error) {
	if !headlessVaultSlot.MatchString(slot) {
		return "", errors.New("Invalid headless credential slot.")
	}
	if err := secureHeadlessProfile(filepath.Dir(v.dir)); err != nil {
		return "", err
	}
	if err := secureHeadlessProfile(v.dir); err != nil {
		return "", err
	}
	return filepath.Join(v.dir, slot), nil
}

func (v headlessVault) Get(slot string) (string, error) {
	path, err := v.path(slot)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", keyring.ErrNotFound
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !headlessFileOwned(info) || info.Size() > 64<<10 {
		return "", errors.New("Cannot read an unsafe headless credential file.")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("Cannot read the private headless credential file.")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm()&0077 != 0 || !headlessFileOwned(opened) {
		return "", errors.New("The private headless credential file changed while opening it.")
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return "", errors.New("Cannot read the private headless credential file.")
	}
	return string(data), nil
}

func (v headlessVault) Set(slot, value string) error {
	path, err := v.path(slot)
	if err != nil {
		return err
	}
	if len(value) > 64<<10 {
		return errors.New("The headless credential exceeds the size limit.")
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !headlessFileOwned(info) {
			return errors.New("Cannot replace an unsafe headless credential file.")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("Cannot inspect the private headless credential file.")
	}
	if err := atomicCatalogFile(path, []byte(value)); err != nil {
		return errors.New("Cannot save the private headless credential file.")
	}
	return nil
}

func (v headlessVault) Delete(slot string) error {
	path, err := v.path(slot)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !headlessFileOwned(info) {
		return errors.New("Cannot remove an unsafe headless credential file.")
	}
	if err := os.Remove(path); err != nil {
		return errors.New("Cannot remove the private headless credential file.")
	}
	return nil
}

func credentialVaultLimit(v credentialVault) int {
	if _, ok := v.(headlessVault); ok {
		return 64 << 10
	}
	return 2400
}
