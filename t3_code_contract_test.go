package main

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writeT3CodeFixtureASAR(t *testing.T, path string, files map[string][]byte) {
	t.Helper()
	tree := t3CodeASAREntry{Files: map[string]t3CodeASAREntry{}}
	keys := []string{}
	for key := range files {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var data []byte
	for _, path := range keys {
		parent := tree
		components := strings.Split(path, "/")
		for _, name := range components[:len(components)-1] {
			child, exists := parent.Files[name]
			if !exists {
				child = t3CodeASAREntry{Files: map[string]t3CodeASAREntry{}}
				parent.Files[name] = child
			}
			parent = child
		}
		parent.Files[components[len(components)-1]] = t3CodeASAREntry{Size: int64(len(files[path])), Offset: fmtT3CodeOffset(len(data))}
		data = append(data, files[path]...)
	}
	header, _ := json.Marshal(tree)
	prefix := make([]byte, 16)
	binary.LittleEndian.PutUint32(prefix[:4], 4)
	binary.LittleEndian.PutUint32(prefix[4:8], uint32(len(header)+8))
	binary.LittleEndian.PutUint32(prefix[8:12], uint32(len(header)+4))
	binary.LittleEndian.PutUint32(prefix[12:16], uint32(len(header)))
	if err := os.WriteFile(path, append(append(prefix, header...), data...), 0600); err != nil {
		t.Fatal(err)
	}
}
func fmtT3CodeOffset(n int) string { data, _ := json.Marshal(n); return string(data) }

func TestT3CodeContractRejectsMissingCapabilitiesWithoutChangingProfiles(t *testing.T) {
	for _, token := range []string{"providerInstances", "homePath", "customModels", "provider-env-", "base64url", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "T3CODE_HOME", "providerModelPreferences", "hiddenModels", "modelOrder"} {
		t.Run(token, func(t *testing.T) {
			a := t3CodeTestApp(t, "linux", "10.9.0-beta.47")
			binary := mustResolveT3CodeTest(t, a)
			archivePath := filepath.Join(filepath.Dir(binary), "resources", "app.asar")
			archive, err := t3CodeOpenASAR(archivePath)
			if err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{}
			for _, path := range []string{"package.json", "apps/desktop/dist-electron/main.cjs", "apps/server/dist/bin.mjs"} {
				files[path], err = archive.read(path, 64<<10)
				if err != nil {
					t.Fatal(err)
				}
				files[path] = []byte(strings.ReplaceAll(string(files[path]), token, "REMOVED"))
			}
			archive.file.Close()
			writeT3CodeFixtureASAR(t, archivePath, files)
			if response := adminRequest(a, "clients/t3-code", `{}`); response.Code != 409 || !strings.Contains(response.Body.String(), "incompatible") {
				t.Fatal(response.Code, response.Body.String())
			}
			if _, err := os.Stat(t3CodePaths(a.dir).Selection); !os.IsNotExist(err) {
				t.Fatal("incompatible app changed private profile", err)
			}
		})
	}
}

func TestT3CodePreparedTracksSourceChangesWithSameVersion(t *testing.T) {
	a := t3CodeTestApp(t, "linux", "0.99.0-canary.12")
	prepareT3CodeFixture(t, a)
	saved, err := a.readT3CodePrepared()
	if err != nil {
		t.Fatal(err)
	}
	binary := mustResolveT3CodeTest(t, a)
	archivePath := filepath.Join(filepath.Dir(binary), "resources", "app.asar")
	archive, err := t3CodeOpenASAR(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, path := range []string{"package.json", "apps/desktop/dist-electron/main.cjs", "apps/server/dist/bin.mjs"} {
		files[path], err = archive.read(path, 64<<10)
		if err != nil {
			t.Fatal(err)
		}
	}
	archive.file.Close()
	files["apps/server/dist/bin.mjs"] = append(files["apps/server/dist/bin.mjs"], []byte(" // in-place upstream update")...)
	writeT3CodeFixtureASAR(t, archivePath, files)
	if a.t3CodeReady(saved, binary, a.launchRuntime()) {
		t.Fatal("same-version source update retained prepared readiness")
	}
	if _, err := a.planClientLaunch(clientLaunchRequest{Client: "t3-code"}, a.launchRuntime()); err == nil {
		t.Fatal("updated app launched stale profiles")
	}
	prepareT3CodeFixture(t, a)
}

func TestT3CodeDiscoversArbitraryChannelNames(t *testing.T) {
	for _, platform := range []string{"macos", "windows", "linux"} {
		t.Run(platform, func(t *testing.T) {
			home := t.TempDir()
			local := filepath.Join(home, "AppData", "Local")
			source := syntheticT3CodeExecutable(t, t.TempDir(), platform, "2.3.4-canary.73")
			var destination string
			if platform == "macos" {
				destination = filepath.Join(home, "Applications", "T3 Code (Canary).app")
				os.MkdirAll(filepath.Dir(destination), 0700)
				if err := os.Rename(source, destination); err != nil {
					t.Fatal(err)
				}
			} else {
				root := filepath.Join(home, "Applications", "T3 Code (Canary)")
				if platform == "windows" {
					root = filepath.Join(local, "Programs", "T3 Code (Canary)")
				}
				os.MkdirAll(filepath.Dir(root), 0700)
				if err := os.Rename(filepath.Dir(source), root); err != nil {
					t.Fatal(err)
				}
				destination = filepath.Join(root, filepath.Base(source))
				if platform == "windows" {
					next := filepath.Join(root, "T3 Code (Canary).exe")
					if err := os.Rename(destination, next); err != nil {
						t.Fatal(err)
					}
					destination = next
				}
			}
			candidates := []string{}
			for _, candidate := range t3CodeDiscoveredCandidates(platform, home, local) {
				if strings.HasPrefix(candidate, home+string(filepath.Separator)) {
					candidates = append(candidates, candidate)
				}
			}
			selected, err := resolveT3CodeCandidates(candidates)
			if err != nil || selected != destination {
				t.Fatal("arbitrary installed channel not discovered", selected, err, candidates)
			}
			if err := t3CodeCompatibility(selected, platform); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestT3CodeInstalledDetectionMetadata(t *testing.T) {
	installed := os.Getenv("KILO_TEST_T3_APP")
	if installed == "" {
		t.Skip("set KILO_TEST_T3_APP to inspect an installed app without executing it")
	}
	info, err := inspectT3CodeInstallation(installed, "darwin")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Detected T3 %s, protocolV2=%t, sourceDigest=%s, packagedClaudeRows=%d", info.Version, info.ProtocolV2, info.SourceDigest, len(info.ClaudeBuiltins))
}

func TestT3CodeRejectsUnrelatedIdentityAndUnsafeMain(t *testing.T) {
	for _, change := range []struct{ name, value string }{{"name", "unrelated-electron-app"}, {"version", ""}, {"main", "../escape.cjs"}, {"main", "/apps/desktop/dist-electron/main.cjs"}, {"main", "C:/apps/main.cjs"}, {"main", "file:apps/main.cjs"}} {
		t.Run(change.name+"/"+change.value, func(t *testing.T) {
			path := syntheticT3CodeExecutable(t, t.TempDir(), "linux", "4.0.0-beta.1")
			archivePath := filepath.Join(filepath.Dir(path), "resources", "app.asar")
			archive, err := t3CodeOpenASAR(archivePath)
			if err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{}
			for _, key := range []string{"package.json", "apps/desktop/dist-electron/main.cjs", "apps/server/dist/bin.mjs"} {
				files[key], err = archive.read(key, 64<<10)
				if err != nil {
					t.Fatal(err)
				}
			}
			archive.file.Close()
			var metadata map[string]string
			if err := json.Unmarshal(files["package.json"], &metadata); err != nil {
				t.Fatal(err)
			}
			metadata[change.name] = change.value
			files["package.json"], _ = json.Marshal(metadata)
			writeT3CodeFixtureASAR(t, archivePath, files)
			if err := t3CodeCompatibility(path, "linux"); err == nil {
				t.Fatal("unrelated identity or unsafe main accepted", change)
			}
		})
	}
}

func TestT3CodePreparedTracksMainOutsideSourceFolders(t *testing.T) {
	a := t3CodeTestApp(t, "linux", "2.0.0-alpha.3")
	binary := mustResolveT3CodeTest(t, a)
	archivePath := filepath.Join(filepath.Dir(binary), "resources", "app.asar")
	archive, err := t3CodeOpenASAR(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, path := range []string{"package.json", "apps/desktop/dist-electron/main.cjs", "apps/server/dist/bin.mjs"} {
		files[path], err = archive.read(path, 64<<10)
		if err != nil {
			t.Fatal(err)
		}
	}
	archive.file.Close()
	var metadata map[string]string
	if err := json.Unmarshal(files["package.json"], &metadata); err != nil {
		t.Fatal(err)
	}
	metadata["main"] = "bootstrap.cjs"
	files["package.json"], _ = json.Marshal(metadata)
	files["bootstrap.cjs"] = []byte("require('original-desktop-main')")
	writeT3CodeFixtureASAR(t, archivePath, files)
	prepareT3CodeFixture(t, a)
	saved, err := a.readT3CodePrepared()
	if err != nil {
		t.Fatal(err)
	}
	files["bootstrap.cjs"] = []byte("require('modified-desktop-main')")
	writeT3CodeFixtureASAR(t, archivePath, files)
	if a.t3CodeReady(saved, binary, a.launchRuntime()) {
		t.Fatal("same-size external main update retained readiness")
	}
}

func TestT3CodeDiscoversChannelExecutableOnPATH(t *testing.T) {
	for _, platform := range []string{"linux", "windows"} {
		t.Run(platform, func(t *testing.T) {
			root := t.TempDir()
			source := syntheticT3CodeExecutable(t, root, platform, "7.0.0-dev.4")
			name := "t3code-custom-channel"
			if platform == "windows" {
				name += ".exe"
			}
			path := filepath.Join(filepath.Dir(source), name)
			if err := os.Rename(source, path); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", filepath.Dir(path))
			candidates := t3CodeDiscoveredCandidates(platform, t.TempDir(), "")
			found := false
			for _, candidate := range candidates {
				found = found || candidate == path
			}
			if !found {
				t.Fatal("channel executable on PATH not discovered", candidates)
			}
			if err := t3CodeCompatibility(path, platform); err != nil {
				t.Fatal(err)
			}
		})
	}
}
