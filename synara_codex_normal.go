package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"
)

const synaraCodexNormalShimName = "kilo-proxy-synara-codex-normal"
const synaraCodexNormalDescriptorName = "kilo-proxy-synara-codex-normal.json"
const synaraCodexNormalHealthLimit = 4 << 10
const synaraCodexNormalHealthTimeout = 3 * time.Second

type synaraCodexNormalDescriptor struct {
	Version int    `json:"version"`
	Binary  string `json:"binary"`
}

func (a *app) synaraAdapterSourceHash() (string, error) {
	if a.synaraAdapterSource == nil {
		// The shipped executable is immutable for this running process. An app
		// update starts a new process and invalidates an older copied adapter.
		return openDesignSourceHash()
	}
	path, err := a.synaraAdapterSource()
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return openDesignFileHash(path, openDesignOpenCodeBinaryLimit)
}

func synaraCodexNormalShimPath(root, platform string) string {
	name := synaraCodexNormalShimName
	if platform == "windows" {
		name += ".exe"
	}
	return filepath.Join(root, "adapters", name)
}

func isSynaraCodexNormalShim(executable, platform string) bool {
	name := filepath.Base(synaraCodexNormalShimPath("", platform))
	if platform == "windows" {
		return strings.EqualFold(filepath.Base(executable), name)
	}
	return filepath.Base(executable) == name
}

// Include both files in Synara's registration transaction. No credential file
// is inspected: this adapter delegates Codex's own authenticated status probe.
func planSynaraCodexNormalShim(root, binary, source, platform string) ([]profileFile, error) {
	failure := errors.New("Cannot safely prepare the private Synara Codex adapter.")
	shim := synaraCodexNormalShimPath(root, platform)
	if safeEditorDir(root, filepath.Dir(shim)) != nil || validateOpenDesignShimDirectory(filepath.Dir(shim)) != nil {
		return nil, failure
	}
	target, err := filepath.EvalSymlinks(binary)
	if err != nil || validateOpenDesignShimBinary(target, platform, true) != nil || isSynaraCodexNormalShim(target, platform) || isOpenDesignOpenCodeShim(target, platform) {
		return nil, failure
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil || validateOpenDesignShimBinary(source, platform, true) != nil {
		return nil, failure
	}
	sourceInfo, _ := os.Stat(source)
	targetInfo, _ := os.Stat(target)
	if sourceInfo == nil || targetInfo == nil || os.SameFile(sourceInfo, targetInfo) {
		return nil, failure
	}
	data, err := readOpenDesignShimFile(source, openDesignOpenCodeBinaryLimit)
	if err != nil {
		return nil, failure
	}
	file, err := prepareSynaraAdapterBinary(shim, data)
	if err != nil {
		return nil, failure
	}
	descriptorData, err := json.Marshal(synaraCodexNormalDescriptor{Version: 1, Binary: target})
	if err != nil || len(descriptorData) > int(openDesignOpenCodeDescriptorLimit) {
		return nil, failure
	}
	descriptor, err := prepareProfileFile(filepath.Join(filepath.Dir(shim), synaraCodexNormalDescriptorName), append(descriptorData, '\n'))
	if err != nil {
		return nil, failure
	}
	return []profileFile{file, descriptor}, nil
}

func prepareSynaraAdapterBinary(path string, data []byte) (profileFile, error) {
	file := profileFile{path: path, new: data, mode: 0700}
	old, err := readOpenDesignShimFile(path, openDesignOpenCodeBinaryLimit)
	if err == nil {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			return file, statErr
		}
		file.old, file.oldMode, file.exists = old, info.Mode().Perm(), true
	} else if !errors.Is(err, os.ErrNotExist) {
		return file, err
	}
	file.changed = !file.exists || !bytes.Equal(old, data) || runtime.GOOS != "windows" && file.oldMode != file.mode
	if file.changed && file.exists {
		if err := validateOpenDesignShimDestination(path+".bak", openDesignOpenCodeBinaryLimit); err != nil {
			return file, err
		}
	}
	return file, nil
}

func synaraCodexNormalFileLimit(relative, platform string) int64 {
	if relative == filepath.Join("adapters", filepath.Base(synaraCodexNormalShimPath("", platform))) {
		return openDesignOpenCodeBinaryLimit
	}
	return catalogLimit
}

func synaraCodexNormalShimTarget(executable, platform string) (string, error) {
	failure := errors.New("Cannot safely read the Synara Codex adapter. Prepare Synara again.")
	dir := filepath.Dir(executable)
	if !isSynaraCodexNormalShim(executable, platform) || validateOpenDesignShimDirectory(dir) != nil || validateOpenDesignShimBinary(executable, platform, true) != nil {
		return "", failure
	}
	data, err := readOpenDesignShimFile(filepath.Join(dir, synaraCodexNormalDescriptorName), openDesignOpenCodeDescriptorLimit)
	if err != nil {
		return "", failure
	}
	if _, err := decodeClaudeDesktopObject(data); err != nil {
		return "", failure
	}
	var descriptor synaraCodexNormalDescriptor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&descriptor) != nil || decoder.Decode(&struct{}{}) != io.EOF || descriptor.Version != 1 || isSynaraCodexNormalShim(descriptor.Binary, platform) || isOpenDesignOpenCodeShim(descriptor.Binary, platform) || validateOpenDesignShimBinary(descriptor.Binary, platform, true) != nil {
		return "", failure
	}
	shimInfo, _ := os.Stat(executable)
	targetInfo, _ := os.Stat(descriptor.Binary)
	if shimInfo == nil || targetInfo == nil || os.SameFile(shimInfo, targetInfo) {
		return "", failure
	}
	return descriptor.Binary, nil
}

// The beta's health parser recognizes the ChatGPT voice capability only in
// JSON. Current Codex emits this exact successful status as plain text instead.
// Its app-server still performs Synara's genuine ChatGPT-authentication check
// before transcription; this conversion does not provide an API-key voice path.
func synaraCodexChatGPTStatus(stdout, stderr []byte) bool {
	out, err := strings.TrimSpace(string(stdout)), strings.TrimSpace(string(stderr))
	return out == "Logged in using ChatGPT" && err == "" || err == "Logged in using ChatGPT" && out == ""
}

func synaraCodexNormalHealthArgs(args []string) bool {
	return reflect.DeepEqual(args, []string{"-c", "mcp_servers={}", "login", "status"})
}

// Keep Buffer private: embedding it would expose io.ReaderFrom and let
// os/exec's io.Copy bypass Write's bound when a status response is oversized.
type synaraCodexHealthOutput struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (output *synaraCodexHealthOutput) Write(data []byte) (int, error) {
	if len(data) > synaraCodexNormalHealthLimit-output.buffer.Len() {
		output.exceeded = true
		return 0, errors.New("Codex health status exceeds its size limit")
	}
	return output.buffer.Write(data)
}

func (output *synaraCodexHealthOutput) Bytes() []byte { return output.buffer.Bytes() }

func synaraCodexNormalHealth(ctx context.Context, binary string, args, environment []string, stdout, stderr io.Writer) int {
	command := exec.CommandContext(ctx, binary, args...)
	command.WaitDelay = 250 * time.Millisecond
	command.Env = environment
	command.Stdin = os.Stdin
	hideOpenDesignProbeWindow(command)
	out := &synaraCodexHealthOutput{}
	errOut := &synaraCodexHealthOutput{}
	command.Stdout, command.Stderr = out, errOut
	err := command.Run()
	if ctx.Err() != nil || out.exceeded || errOut.exceeded {
		fmt.Fprintln(stderr, "Cannot safely verify Codex login status for Synara.")
		return 1
	}
	if err == nil && synaraCodexChatGPTStatus(out.Bytes(), errOut.Bytes()) {
		fmt.Fprintln(stdout, `{"authenticated":true,"authMethod":"chatgpt"}`)
		return 0
	}
	_, _ = stdout.Write(out.Bytes())
	_, _ = stderr.Write(errOut.Bytes())
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode()
	}
	return 126
}

// Dispatch before Kilo's flags. Except for the exact health request, arguments,
// environment, working directory, stdio and native app-server remain unchanged.
func runSynaraCodexNormalShim() (bool, int) {
	executable, err := os.Executable()
	if err != nil || !isSynaraCodexNormalShim(executable, runtime.GOOS) {
		return false, 0
	}
	target, err := synaraCodexNormalShimTarget(executable, runtime.GOOS)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return true, 126
	}
	args, environment := os.Args[1:], os.Environ()
	if synaraCodexNormalHealthArgs(args) {
		ctx, cancel := context.WithTimeout(context.Background(), synaraCodexNormalHealthTimeout)
		defer cancel()
		return true, synaraCodexNormalHealth(ctx, target, args, environment, os.Stdout, os.Stderr)
	}
	code, err := execOpenDesignOpenCode(target, args, environment)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot start the configured Codex executable. Prepare Synara again.")
		return true, 126
	}
	return true, code
}
