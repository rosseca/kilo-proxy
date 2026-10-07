package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	synaraPrivateRuntimeRevision = 2
	synaraRuntimeSourceLimit     = 16 << 20
	synaraRuntimeManifestName    = "runtime.json"
	synaraRuntimeServerName      = "server.mjs"
	synaraRuntimeHookName        = "backend-hook.mjs"
	synaraRuntimeBundleName      = "Synara Kilo Runtime.app"
	synaraDesktopSpawnAnchor     = "const child = node_child_process.spawn(process.execPath, [...backendNodeArgs(), backendEntry], {"
)

// Only public model descriptors and source identities enter this record. The
// original signed installation remains the source of modules and migrations.
type synaraPrivateRuntime struct {
	Revision            int               `json:"revision"`
	Root                string            `json:"root"`
	Executable          string            `json:"executable"`
	SourceApp           string            `json:"sourceApp"`
	SourceArchive       string            `json:"sourceArchive"`
	SourceMainEntry     string            `json:"sourceMainEntry"`
	SourceMainSHA256    string            `json:"sourceMainSHA256"`
	SourceServerSHA256  string            `json:"sourceServerSHA256"`
	ArchiveHeaderSHA256 string            `json:"archiveHeaderSHA256"`
	Files               map[string]string `json:"files"`
}

func synaraRuntimeFileNames(exeName string) []string {
	return []string{synaraRuntimeHookName, synaraRuntimeServerName, filepath.Join(synaraRuntimeBundleName, "Contents", "Info.plist"), filepath.Join(synaraRuntimeBundleName, "Contents", "Resources", "app.asar"), filepath.Join(synaraRuntimeBundleName, "Contents", "MacOS", exeName), filepath.Join(synaraRuntimeBundleName, "Contents", "_CodeSignature", "CodeResources")}
}

func synaraRuntimeIdentity(binary, header, privateRoot, mainEntry, mainDigest, sourceServerDigest, serverDigest string) []byte {
	archive := filepath.Join(binary, "Contents", "Resources", "app.asar")
	// Use fixed path placeholders to bind the generator without a circular
	// dependency on the cache directory that the generated code will contain.
	bootstrap := synaraRuntimeBootstrap(archive, mainEntry, "__KILO_SYNARA_RUNTIME_HOOK__", mainDigest)
	hook := synaraRuntimeBackendHook(archive, "__KILO_SYNARA_RUNTIME_SERVER__", sourceServerDigest, serverDigest)
	identity, _ := json.Marshal([]any{synaraPrivateRuntimeRevision, binary, header, privateRoot, mainEntry, mainDigest, sourceServerDigest, serverDigest, openDesignHash(bootstrap), openDesignHash(hook)})
	return identity
}

func synaraReadASARSource(archive, name string, limit int64) ([]byte, string, error) {
	if filepath.IsAbs(name) || filepath.ToSlash(filepath.Clean(name)) != name || strings.HasPrefix(name, "../") {
		return nil, "", errors.New("Invalid private Synara source entry.")
	}
	f, err := os.Open(archive)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", errors.New("Invalid Synara archive.")
	}
	var prefix [16]byte
	if _, err = io.ReadFull(f, prefix[:]); err != nil {
		return nil, "", err
	}
	headerSize, jsonSize := int64(binary.LittleEndian.Uint32(prefix[4:8])), int64(binary.LittleEndian.Uint32(prefix[12:16]))
	if binary.LittleEndian.Uint32(prefix[:4]) != 4 || headerSize < 8 || headerSize > 8<<20 || jsonSize < 2 || jsonSize > headerSize-8 || headerSize+8 > info.Size() {
		return nil, "", errors.New("Invalid Synara archive header.")
	}
	header := make([]byte, jsonSize)
	if _, err = io.ReadFull(f, header); err != nil {
		return nil, "", err
	}
	var tree t3CodeASAREntry
	if json.Unmarshal(header, &tree) != nil {
		return nil, "", errors.New("Invalid Synara archive index.")
	}
	entry := tree
	for _, part := range strings.Split(name, "/") {
		var ok bool
		entry, ok = entry.Files[part]
		if !ok || entry.Link != "" || entry.Unpacked {
			return nil, "", errors.New("Synara source entry is unavailable.")
		}
	}
	offset, err := strconv.ParseInt(entry.Offset, 10, 64)
	base := headerSize + 8
	if err != nil || offset < 0 || entry.Size < 1 || entry.Size > limit || offset > info.Size()-base || entry.Size > info.Size()-base-offset {
		return nil, "", errors.New("Invalid Synara source entry size.")
	}
	data := make([]byte, entry.Size)
	if _, err = f.ReadAt(data, base+offset); err != nil {
		return nil, "", err
	}
	return data, openDesignHash(header), nil
}

func synaraRuntimeASAR(files map[string][]byte) ([]byte, string, error) {
	// A tiny bootstrap package, not a rewritten copy of the installed archive.
	tree := map[string]any{"files": map[string]any{}}
	entries := tree["files"].(map[string]any)
	payload := []byte{}
	for _, name := range []string{"package.json", "bootstrap.cjs"} {
		data, ok := files[name]
		if !ok || len(data) == 0 || len(data) > 256<<10 {
			return nil, "", errors.New("Invalid private Synara bootstrap.")
		}
		digest := openDesignHash(data)
		entries[name] = map[string]any{"size": len(data), "offset": strconv.Itoa(len(payload)), "integrity": map[string]any{"algorithm": "SHA256", "hash": digest, "blockSize": 4194304, "blocks": []string{digest}}}
		payload = append(payload, data...)
	}
	header, err := json.Marshal(tree)
	if err != nil {
		return nil, "", err
	}
	padding := (4 - len(header)%4) % 4
	headerPickle := make([]byte, 8+len(header)+padding)
	binary.LittleEndian.PutUint32(headerPickle[:4], uint32(len(headerPickle)-4))
	binary.LittleEndian.PutUint32(headerPickle[4:8], uint32(len(header)))
	copy(headerPickle[8:], header)
	data := make([]byte, 8, 8+len(headerPickle)+len(payload))
	binary.LittleEndian.PutUint32(data[:4], 4)
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(headerPickle)))
	data = append(data, headerPickle...)
	data = append(data, payload...)
	return data, openDesignHash(header), nil
}

func synaraRuntimeBootstrap(sourceArchive, mainEntry, hook, mainDigest string) []byte {
	encode := func(value string) string { b, _ := json.Marshal(value); return string(b) }
	main := filepath.Join(sourceArchive, filepath.FromSlash(mainEntry))
	replacement := "const child = node_child_process.spawn(process.execPath, [...backendNodeArgs(), \"--import\", " + encode(hook) + ", backendEntry], {"
	return []byte("'use strict';\nconst fs=require('node:fs'),crypto=require('node:crypto'),Module=require('node:module'),path=require('node:path'),{app}=require('electron');\n" +
		"const originalMain=" + encode(main) + ", originalRoot=" + encode(sourceArchive) + ";\n" +
		"let source=fs.readFileSync(originalMain,'utf8');\n" +
		"if(Buffer.byteLength(source)>16777216||crypto.createHash('sha256').update(source).digest('hex')!==" + encode(mainDigest) + ")throw Error('Synara desktop source changed; prepare again.');\n" +
		"const anchor=" + encode(synaraDesktopSpawnAnchor) + ";if(source.split(anchor).length!==2)throw Error('Synara backend startup changed; prepare again.');\n" +
		"source=source.replace(anchor," + encode(replacement) + ");app.getAppPath=()=>originalRoot;\n" +
		"const originalModule=new Module(originalMain,module);originalModule.filename=originalMain;originalModule.paths=Module._nodeModulePaths(path.dirname(originalMain));originalModule._compile(source,originalMain);\n")
}

func synaraRuntimeBackendHook(sourceArchive, server, sourceDigest, digest string) []byte {
	encode := func(value string) string { b, _ := json.Marshal(value); return string(b) }
	entry := filepath.Join(sourceArchive, filepath.FromSlash("apps/server/dist/index.mjs"))
	return []byte("import fs from 'node:fs';import crypto from 'node:crypto';import {registerHooks} from 'node:module';import {pathToFileURL} from 'node:url';\n" +
		"const entry=pathToFileURL(" + encode(entry) + ").href,patched=" + encode(server) + ";\n" +
		"registerHooks({load(url,context,nextLoad){const loaded=nextLoad(url,context);if(url!==entry)return loaded;const original=loaded.source;if(original==null||Buffer.byteLength(original)>16777216||crypto.createHash('sha256').update(original).digest('hex')!==" + encode(sourceDigest) + ")throw Error('Synara server source changed; prepare again.');\n" +
		"const stat=fs.lstatSync(patched);if(!stat.isFile()||stat.isSymbolicLink()||stat.size>16777216)throw Error('Invalid private Synara server.');const source=fs.readFileSync(patched);if(crypto.createHash('sha256').update(source).digest('hex')!==" + encode(digest) + ")throw Error('Private Synara server changed; prepare again.');return {...loaded,source};}});\n")
}

// Copy only owned runtime files. macOS clonefile copies use copy-on-write disk
// blocks; all code-signing destinations remain inside the private directory.
func synaraCloneRuntimeItem(source, destination, installation string) error {
	canonicalInstallation, err := filepath.EvalSymlinks(installation)
	if err != nil {
		return err
	}
	count, total := 0, int64(0)
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		count++
		if count > 20000 {
			return errors.New("Synara runtime installation contains too many files.")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil || filepath.IsAbs(link) {
				return errors.New("Synara runtime contains an absolute resource link.")
			}
			real, err := filepath.EvalSymlinks(path)
			if err != nil || !withinT3CodePath(canonicalInstallation, real) {
				return errors.New("Synara runtime contains an external resource link.")
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("Synara runtime contains an unsupported resource.")
		}
		if info.Mode().IsRegular() {
			total += info.Size()
			if info.Size() > 512<<20 || total > 2<<30 {
				return errors.New("Synara runtime installation is too large.")
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/cp", "-cR", source, destination)
	if command.Run() != nil {
		return errors.New("Could not clone the private Synara runtime.")
	}
	return nil
}

func synaraOwnedRuntimeLinksSafe(bundle string) error {
	info, err := os.Lstat(bundle)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("The private Synara runtime must be an owned directory.")
	}
	canonicalBundle, err := filepath.EvalSymlinks(bundle)
	if err != nil {
		return err
	}
	return filepath.WalkDir(bundle, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return nil
		}
		link, err := os.Readlink(path)
		if err != nil || filepath.IsAbs(link) {
			return errors.New("The private Synara runtime contains an absolute resource link.")
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil || !withinT3CodePath(canonicalBundle, real) {
			return errors.New("The private Synara runtime contains an external resource link.")
		}
		return nil
	})
}

func synaraRuntimeInfoPlist(original []byte, originalHash, bootstrapHash string) ([]byte, error) {
	oldID := []byte("<string>com.emanueledipietro.synara.beta</string>")
	if len(original) > 1<<20 || bytes.Count(original, []byte(originalHash)) != 1 || bytes.Count(original, oldID) != 1 {
		return nil, errors.New("This Synara desktop bundle metadata has not been validated.")
	}
	data := bytes.Replace(original, []byte(originalHash), []byte(bootstrapHash), 1)
	return bytes.Replace(data, oldID, []byte("<string>ai.kilo.synara-private-runtime</string>"), 1), nil
}

func synaraRuntimePackageData(source []byte) ([]byte, string, error) {
	var fields map[string]json.RawMessage
	var metadata synaraPackageInfo
	if len(source) == 0 || len(source) > 64<<10 || json.Unmarshal(source, &fields) != nil || json.Unmarshal(source, &metadata) != nil || metadata.Name != "synara-desktop-beta" || metadata.SynaraDesktopFlavor != "beta" {
		return nil, "", errors.New("Cannot verify the Synara Beta runtime package.")
	}
	main := metadata.Main
	if main == "" || main == "." || main == ".." || filepath.IsAbs(main) || filepath.ToSlash(filepath.Clean(main)) != main || strings.HasPrefix(main, "../") || strings.ContainsAny(main, "\\\x00") {
		return nil, "", errors.New("Invalid Synara desktop source entry.")
	}
	// Keep the installed release's identity, including its real version, commit,
	// dependencies and future metadata. Only the private bootstrap entry changes.
	fields["main"] = json.RawMessage(`"bootstrap.cjs"`)
	data, err := json.Marshal(fields)
	return data, main, err
}

func synaraSourceDigestValid(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	for _, c := range digest {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func prepareSynaraPrivateRuntime(options synaraProfileOptions, binary, platform string) (synaraPrivateRuntime, error) {
	// This beta's embedded archive validation differs across operating systems.
	// Preserve the existing integration on other platforms until their private
	// native bootstrap is validated; never disable Electron integrity fuses.
	if platform != "macos" && platform != "darwin" {
		return synaraPrivateRuntime{}, nil
	}
	var empty synaraPrivateRuntime
	if !strings.HasSuffix(binary, ".app") || t3CodeBundleExecutable(binary) == "" {
		return empty, errors.New("Invalid Synara desktop bundle.")
	}
	archive := filepath.Join(binary, "Contents", "Resources", "app.asar")
	packageSource, header, err := synaraReadASARSource(archive, "package.json", 64<<10)
	if err != nil {
		return empty, errors.New("Cannot read the Synara Beta runtime package.")
	}
	packageData, mainEntry, err := synaraRuntimePackageData(packageSource)
	if err != nil {
		return empty, err
	}
	main, mainHeader, err := synaraReadASARSource(archive, mainEntry, synaraRuntimeSourceLimit)
	if err != nil || mainHeader != header || strings.Count(string(main), synaraDesktopSpawnAnchor) != 1 {
		return empty, errors.New("This Synara desktop does not expose the private backend startup contract; no private changes saved.")
	}
	server, serverHeader, err := synaraReadASARSource(archive, "apps/server/dist/index.mjs", synaraRuntimeSourceLimit)
	if err != nil || serverHeader != header {
		return empty, errors.New("Cannot verify the Synara server source.")
	}
	patched, err := patchSynaraDelegationServer(server, options)
	if err != nil {
		return empty, err
	}
	mainDigest, serverDigest := openDesignHash(main), openDesignHash(server)
	identity := synaraRuntimeIdentity(binary, header, options.RootDir, mainEntry, mainDigest, serverDigest, openDesignHash(patched))
	cache := filepath.Join(options.RootDir, "runtime")
	if err = safeEditorDir(options.RootDir, cache); err != nil {
		return empty, err
	}
	root := filepath.Join(cache, openDesignHash(identity))
	if _, err = os.Lstat(root); err == nil {
		data, readErr := readOpenDesignShimFile(filepath.Join(root, synaraRuntimeManifestName), 64<<10)
		var saved synaraPrivateRuntime
		if readErr == nil && json.Unmarshal(data, &saved) == nil && saved.Root == root && synaraPrivateRuntimeReady(options, binary, platform, saved) {
			return saved, nil
		}
		return empty, errors.New("The cached private Synara runtime changed. Preserve it for inspection and prepare a fresh profile.")
	} else if !errors.Is(err, os.ErrNotExist) {
		return empty, err
	}
	stage, err := os.MkdirTemp(cache, ".prepare-")
	if err != nil {
		return empty, err
	}
	defer os.RemoveAll(stage)
	if err = os.Chmod(stage, 0700); err != nil {
		return empty, err
	}
	bundle := filepath.Join(stage, synaraRuntimeBundleName)
	contents := filepath.Join(bundle, "Contents")
	for _, dir := range []string{filepath.Join(contents, "MacOS"), filepath.Join(contents, "Resources")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return empty, err
		}
	}
	originalContents := filepath.Join(binary, "Contents")
	originalExecutable := t3CodeBundleExecutable(binary)
	exeName := filepath.Base(originalExecutable)
	for _, pair := range [][2]string{{originalExecutable, filepath.Join(contents, "MacOS", exeName)}, {filepath.Join(originalContents, "Frameworks"), filepath.Join(contents, "Frameworks")}, {filepath.Join(originalContents, "Helpers"), filepath.Join(contents, "Helpers")}} {
		if err = synaraCloneRuntimeItem(pair[0], pair[1], binary); err != nil {
			return empty, err
		}
	}
	resources, err := os.ReadDir(filepath.Join(originalContents, "Resources"))
	if err != nil {
		return empty, err
	}
	for _, entry := range resources {
		if entry.Name() == "app.asar" || entry.Name() == "app.asar.unpacked" {
			continue
		}
		if err = synaraCloneRuntimeItem(filepath.Join(originalContents, "Resources", entry.Name()), filepath.Join(contents, "Resources", entry.Name()), binary); err != nil {
			return empty, err
		}
	}
	bootstrap := synaraRuntimeBootstrap(archive, mainEntry, filepath.Join(root, synaraRuntimeHookName), mainDigest)
	bootstrapASAR, bootstrapHeader, err := synaraRuntimeASAR(map[string][]byte{"package.json": packageData, "bootstrap.cjs": bootstrap})
	if err != nil {
		return empty, err
	}
	info, err := readOpenDesignShimFile(filepath.Join(originalContents, "Info.plist"), 1<<20)
	if err != nil {
		return empty, err
	}
	info, err = synaraRuntimeInfoPlist(info, header, bootstrapHeader)
	if err != nil {
		return empty, err
	}
	for path, data := range map[string][]byte{filepath.Join(contents, "Info.plist"): info, filepath.Join(contents, "Resources", "app.asar"): bootstrapASAR, filepath.Join(stage, synaraRuntimeServerName): patched, filepath.Join(stage, synaraRuntimeHookName): synaraRuntimeBackendHook(archive, filepath.Join(root, synaraRuntimeServerName), serverDigest, openDesignHash(patched))} {
		if err = os.WriteFile(path, data, 0600); err != nil {
			return empty, err
		}
	}
	if err = synaraCloneRuntimeItem(filepath.Join(originalContents, "PkgInfo"), filepath.Join(contents, "PkgInfo"), binary); err != nil {
		return empty, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Every nested code file was cloned above; this never traverses an external
	// resource link or signs the original installation.
	if err = synaraOwnedRuntimeLinksSafe(bundle); err != nil {
		return empty, err
	}
	if exec.CommandContext(ctx, "/usr/bin/codesign", "--force", "--deep", "--sign", "-", bundle).Run() != nil || exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", bundle).Run() != nil {
		return empty, errors.New("Could not sign and verify the owned private Synara runtime.")
	}
	result := synaraPrivateRuntime{Revision: synaraPrivateRuntimeRevision, Root: root, Executable: filepath.Join(root, synaraRuntimeBundleName, "Contents", "MacOS", exeName), SourceApp: binary, SourceArchive: archive, SourceMainEntry: mainEntry, SourceMainSHA256: mainDigest, SourceServerSHA256: serverDigest, ArchiveHeaderSHA256: header, Files: map[string]string{}}
	for _, relative := range synaraRuntimeFileNames(exeName) {
		digest, err := openDesignFileHash(filepath.Join(stage, relative), synaraRuntimeSourceLimit)
		if err != nil {
			return empty, err
		}
		result.Files[relative] = digest
	}
	manifest, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return empty, err
	}
	if err = os.WriteFile(filepath.Join(stage, synaraRuntimeManifestName), append(manifest, '\n'), 0600); err != nil {
		return empty, err
	}
	if err = os.Rename(stage, root); err != nil {
		return empty, err
	}
	return result, nil
}

func synaraPrivateRuntimeReady(options synaraProfileOptions, binary, platform string, saved synaraPrivateRuntime) bool {
	if platform != "macos" && platform != "darwin" {
		return saved.Revision == 0 && saved.Root == "" && saved.Executable == "" && saved.SourceApp == "" && saved.SourceArchive == "" && saved.SourceMainEntry == "" && saved.SourceMainSHA256 == "" && saved.SourceServerSHA256 == "" && saved.ArchiveHeaderSHA256 == "" && len(saved.Files) == 0
	}
	if saved.Revision != synaraPrivateRuntimeRevision || saved.SourceApp != binary || saved.SourceArchive != filepath.Join(binary, "Contents", "Resources", "app.asar") || !synaraSourceDigestValid(saved.SourceMainSHA256) || !synaraSourceDigestValid(saved.SourceServerSHA256) || !safeLaunchDir(saved.Root, options.RootDir) || !withinT3CodePath(filepath.Join(options.RootDir, "runtime"), saved.Root) || len(saved.Files) != 6 {
		return false
	}
	exeName := filepath.Base(t3CodeBundleExecutable(binary))
	for _, relative := range synaraRuntimeFileNames(exeName) {
		if saved.Files[relative] == "" {
			return false
		}
	}
	expectedRoot := filepath.Join(options.RootDir, "runtime", openDesignHash(synaraRuntimeIdentity(binary, saved.ArchiveHeaderSHA256, options.RootDir, saved.SourceMainEntry, saved.SourceMainSHA256, saved.SourceServerSHA256, saved.Files[synaraRuntimeServerName])))
	if saved.Root != expectedRoot || saved.Executable != filepath.Join(saved.Root, synaraRuntimeBundleName, "Contents", "MacOS", exeName) {
		return false
	}
	// Reading the bounded archive index and metadata entry detects installation
	// replacement without hashing its entire 191 MB contents per request.
	packageSource, header, err := synaraReadASARSource(saved.SourceArchive, "package.json", 64<<10)
	if err != nil || header != saved.ArchiveHeaderSHA256 {
		return false
	}
	_, mainEntry, err := synaraRuntimePackageData(packageSource)
	if err != nil || mainEntry != saved.SourceMainEntry {
		return false
	}
	for relative, digest := range saved.Files {
		if filepath.IsAbs(relative) || filepath.Clean(relative) != relative || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return false
		}
		path := filepath.Join(saved.Root, relative)
		if !safeLaunchDir(filepath.Dir(path), options.RootDir) {
			return false
		}
		actual, err := openDesignFileHash(path, synaraRuntimeSourceLimit)
		if err != nil || actual != digest {
			return false
		}
	}
	data, err := readOpenDesignShimFile(filepath.Join(saved.Root, synaraRuntimeManifestName), 64<<10)
	if err != nil {
		return false
	}
	var manifest synaraPrivateRuntime
	if json.Unmarshal(data, &manifest) != nil {
		return false
	}
	left, _ := json.Marshal(saved)
	right, _ := json.Marshal(manifest)
	return bytes.Equal(left, right)
}

func validateSynaraPrivateRuntimeLaunch(saved synaraPrivateRuntime) error {
	if saved.Revision == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--deep", "--strict", filepath.Join(saved.Root, synaraRuntimeBundleName)).Run() != nil {
		return errors.New("The private Synara runtime signature changed. Prepare again before launching.")
	}
	return nil
}
