package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const openDesignOpenCodeShimName = "kilo-proxy-opencode"
const openDesignOpenCodeDescriptorName = "kilo-proxy-opencode.json"
const openDesignOpenCodeBinaryLimit int64 = 512 << 20
const openDesignOpenCodeDescriptorLimit int64 = 16 << 10

type openDesignOpenCodeDescriptor struct {
	Version int    `json:"version"`
	Binary  string `json:"binary"`
	Config  string `json:"config"`
}

func openDesignOpenCodeShimPath(dir string) string {
	return openDesignOpenCodeShimPathFor(dir, runtime.GOOS)
}

func openDesignOpenCodeShimPathFor(dir, platform string) string {
	name := openDesignOpenCodeShimName
	if platform == "windows" {
		name += ".exe"
	}
	return filepath.Join(dir, name)
}

// The caller holds the profile/launch lock and has already created this private
// engine directory and opencode.json. The copy reuses Kilo's shipped native
// executable; end users need neither a compiler nor a shell wrapper.
func prepareOpenDesignOpenCodeShim(dir, binary string) (string, error) {
	source, err := os.Executable()
	if err != nil {
		return "", errors.New("Cannot locate Kilo Proxy's native OpenCode adapter.")
	}
	return prepareOpenDesignOpenCodeShimFrom(dir, binary, source, runtime.GOOS)
}

func prepareOpenDesignOpenCodeShimFrom(dir, binaryPath, source, platform string) (string, error) {
	if err := validateOpenDesignShimDirectory(dir); err != nil {
		return "", err
	}
	if _, err := readOpenDesignShimFile(filepath.Join(dir, "opencode.json"), catalogLimit); err != nil {
		return "", errors.New("Prepare a regular private opencode.json before preparing the OpenCode adapter.")
	}
	if !validOpenDesignShimPath(binaryPath) {
		return "", errors.New("Open Design requires a detected local OpenCode executable.")
	}
	// Package-manager links are resolved once, so the descriptor records the
	// detected executable rather than a mutable PATH lookup or shell expression.
	target, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		return "", errors.New("Cannot resolve the detected OpenCode executable.")
	}
	if err := validateOpenDesignShimBinary(target, platform, false); err != nil {
		return "", err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil || validateOpenDesignShimBinary(source, platform, true) != nil {
		return "", errors.New("Cannot safely read Kilo Proxy's native OpenCode adapter.")
	}
	sourceInfo, _ := os.Stat(source)
	targetInfo, _ := os.Stat(target)
	if sourceInfo == nil || targetInfo == nil || os.SameFile(sourceInfo, targetInfo) || isOpenDesignOpenCodeShim(target, platform) {
		return "", errors.New("Choose the installed OpenCode executable, not the Kilo Proxy adapter.")
	}
	shim := openDesignOpenCodeShimPathFor(dir, platform)
	descriptor := filepath.Join(dir, openDesignOpenCodeDescriptorName)
	if err := validateOpenDesignShimDestination(shim, openDesignOpenCodeBinaryLimit); err != nil {
		return "", err
	}
	if err := validateOpenDesignShimDestination(descriptor, openDesignOpenCodeDescriptorLimit); err != nil {
		return "", err
	}
	data, err := json.Marshal(openDesignOpenCodeDescriptor{Version: 1, Binary: target, Config: "opencode.json"})
	if err != nil || len(data) > int(openDesignOpenCodeDescriptorLimit) {
		return "", errors.New("The OpenCode adapter descriptor exceeds its size limit.")
	}
	if err := copyOpenDesignShimBinary(source, shim); err != nil {
		return "", errors.New("Cannot save the native OpenCode adapter. Close Open Design and try again.")
	}
	if err := atomicCatalogFile(descriptor, append(data, '\n')); err != nil {
		return "", errors.New("Cannot save the OpenCode adapter descriptor.")
	}
	return shim, nil
}

func validOpenDesignShimPath(path string) bool {
	return filepath.IsAbs(path) && len(path) <= 8192 && !strings.ContainsAny(path, "\x00\r\n")
}

func validateOpenDesignShimDirectory(dir string) error {
	if !validOpenDesignShimPath(dir) {
		return errors.New("The OpenCode adapter requires an absolute private profile directory.")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("The OpenCode adapter profile must be a real directory, not a symbolic link.")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return errors.New("The OpenCode adapter profile directory must be private (0700).")
	}
	return nil
}

func validateOpenDesignShimDestination(path string, limit int64) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return errors.New("OpenCode adapter files must be bounded regular files, not symbolic links.")
	}
	return nil
}

func readOpenDesignShimFile(path string, limit int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit {
		return nil, errors.New("unsafe OpenCode adapter file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return nil, errors.New("OpenCode adapter file changed during inspection")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("cannot read bounded OpenCode adapter file")
	}
	return data, nil
}

func validateOpenDesignShimBinary(path, platform string, native bool) error {
	if !validOpenDesignShimPath(path) {
		return errors.New("Open Design requires an absolute OpenCode executable path.")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > openDesignOpenCodeBinaryLimit || info.Size() == 0 || platform != "windows" && info.Mode().Perm()&0111 == 0 {
		return errors.New("Open Design requires a regular executable OpenCode binary.")
	}
	if platform == "windows" && !strings.EqualFold(filepath.Ext(path), ".exe") {
		return errors.New("Open Design requires the native OpenCode .exe on Windows; .cmd and .bat launchers are not supported.")
	}
	if !native && platform != "windows" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return errors.New("Cannot inspect the native OpenCode adapter executable.")
	}
	defer f.Close()
	var header [64]byte
	if _, err := io.ReadFull(f, header[:]); err != nil {
		return errors.New("The OpenCode adapter requires a native executable.")
	}
	if platform == "windows" {
		offset := int64(binary.LittleEndian.Uint32(header[60:64]))
		var signature [4]byte
		if !bytes.Equal(header[:2], []byte("MZ")) || offset < 64 || offset > info.Size()-4 {
			return errors.New("Open Design requires a native Windows OpenCode .exe.")
		}
		if _, err := f.ReadAt(signature[:], offset); err != nil || !bytes.Equal(signature[:], []byte("PE\x00\x00")) {
			return errors.New("Open Design requires a native Windows OpenCode .exe.")
		}
		return nil
	}
	magic := binary.BigEndian.Uint32(header[:4])
	if magic != 0x7f454c46 && magic != 0xfeedface && magic != 0xcefaedfe && magic != 0xfeedfacf && magic != 0xcffaedfe && magic != 0xcafebabe && magic != 0xbebafeca && magic != 0xcafebabf && magic != 0xbfbafeca {
		return errors.New("The OpenCode adapter requires a native executable.")
	}
	return nil
}

func copyOpenDesignShimBinary(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	before, err := input.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() > openDesignOpenCodeBinaryLimit {
		return errors.New("unsafe adapter source")
	}
	output, err := os.CreateTemp(filepath.Dir(destination), ".opencode-adapter-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	defer output.Close()
	n, err := io.Copy(output, io.LimitReader(input, openDesignOpenCodeBinaryLimit+1))
	if err != nil || n != before.Size() || n > openDesignOpenCodeBinaryLimit {
		return errors.New("adapter source changed or exceeds its size limit")
	}
	if err = output.Chmod(0700); err != nil {
		return err
	}
	if err = output.Sync(); err != nil {
		return err
	}
	if err = output.Close(); err != nil {
		return err
	}
	if err = validateOpenDesignShimDestination(destination, openDesignOpenCodeBinaryLimit); err != nil {
		return err
	}
	return os.Rename(output.Name(), destination)
}

func isOpenDesignOpenCodeShim(executable, platform string) bool {
	name := filepath.Base(openDesignOpenCodeShimPathFor("", platform))
	if platform == "windows" {
		return strings.EqualFold(filepath.Base(executable), name)
	}
	return filepath.Base(executable) == name
}

// This dispatch must run before Kilo's own flag parsing. There is no argument
// convention to reinterpret: every argument belongs to OpenCode, including
// prompts, and its inherited CONFIG_CONTENT carries Open Design's MCP tools.
func runOpenDesignOpenCodeShim() (bool, int) {
	executable, err := os.Executable()
	if err != nil || !isOpenDesignOpenCodeShim(executable, runtime.GOOS) {
		return false, 0
	}
	binaryPath, environment, err := openDesignOpenCodeShimInvocation(executable, os.Environ(), runtime.GOOS)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return true, 126
	}
	code, err := execOpenDesignOpenCode(binaryPath, os.Args[1:], environment)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot start the configured OpenCode executable. Prepare Open Design again.")
		return true, 126
	}
	return true, code
}

func openDesignOpenCodeShimInvocation(executable string, environment []string, platform string) (string, []string, error) {
	failure := errors.New("Cannot safely read the OpenCode adapter profile. Prepare Open Design again.")
	dir := filepath.Dir(executable)
	if validateOpenDesignShimDirectory(dir) != nil || !isOpenDesignOpenCodeShim(executable, platform) || validateOpenDesignShimBinary(executable, platform, true) != nil {
		return "", nil, failure
	}
	data, err := readOpenDesignShimFile(filepath.Join(dir, openDesignOpenCodeDescriptorName), openDesignOpenCodeDescriptorLimit)
	if err != nil {
		return "", nil, failure
	}
	var descriptor openDesignOpenCodeDescriptor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&descriptor) != nil || decoder.Decode(&struct{}{}) != io.EOF || descriptor.Version != 1 || descriptor.Config != "opencode.json" || isOpenDesignOpenCodeShim(descriptor.Binary, platform) || validateOpenDesignShimBinary(descriptor.Binary, platform, false) != nil {
		return "", nil, failure
	}
	config := filepath.Join(dir, descriptor.Config)
	if _, err = readOpenDesignShimFile(config, catalogLimit); err != nil {
		return "", nil, failure
	}
	return descriptor.Binary, clientChildEnvironment(environment, map[string]string{"OPENCODE_CONFIG": config}, nil, platform), nil
}
