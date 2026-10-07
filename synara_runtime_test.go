package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSynaraPrivateRuntimeBootstrapArchive(t *testing.T) {
	packageData := []byte(`{"name":"synara-desktop-beta","main":"bootstrap.cjs"}`)
	mainDigest := strings.Repeat("c", 64)
	bootstrap := synaraRuntimeBootstrap("/Applications/Synara Beta.app/Contents/Resources/app.asar", "apps/desktop/dist-electron/main.js", "/private/tmp/owned/backend-hook.mjs", mainDigest)
	archive, header, err := synaraRuntimeASAR(map[string][]byte{"package.json": packageData, "bootstrap.cjs": bootstrap})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "app.asar")
	if err := os.WriteFile(path, archive, 0600); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string][]byte{"package.json": packageData, "bootstrap.cjs": bootstrap} {
		actual, gotHeader, err := synaraReadASARSource(path, name, 256<<10)
		if err != nil || !bytes.Equal(actual, want) || gotHeader != header {
			t.Fatalf("bootstrap archive entry %s failed", name)
		}
	}
	for _, fragment := range []string{`app.getAppPath=()=>originalRoot`, mainDigest, `\"--import\"`, `originalModule._compile(source,originalMain)`} {
		if !bytes.Contains(bootstrap, []byte(fragment)) {
			t.Fatalf("bootstrap missing verified contract %q", fragment)
		}
	}
	if _, _, err = synaraReadASARSource(path, "../package.json", 256<<10); err == nil {
		t.Fatal("archive traversal accepted")
	}
	if _, _, err = synaraReadASARSource(path, "bootstrap.cjs", 1); err == nil {
		t.Fatal("oversized archive entry accepted")
	}
	broken := append([]byte{}, archive...)
	binary.LittleEndian.PutUint32(broken[4:8], 1<<30)
	if err = os.WriteFile(path, broken, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = synaraReadASARSource(path, "package.json", 256<<10); err == nil {
		t.Fatal("oversized archive index accepted")
	}
}

func TestSynaraPrivateRuntimeMetadataAndScopedHook(t *testing.T) {
	old := strings.Repeat("a", 64)
	next := strings.Repeat("b", 64)
	plist := []byte(`<plist><dict><key>CFBundleIdentifier</key><string>com.emanueledipietro.synara.beta</string><key>ElectronAsarIntegrity</key><dict><key>Resources/app.asar</key><dict><key>hash</key><string>` + old + `</string></dict></dict></dict></plist>`)
	patched, err := synaraRuntimeInfoPlist(plist, old, next)
	if err != nil || bytes.Contains(patched, []byte(old)) || !bytes.Contains(patched, []byte(next)) || !bytes.Contains(patched, []byte("ai.kilo.synara-private-runtime")) {
		t.Fatal("private bundle metadata correction failed")
	}
	if !bytes.Contains(plist, []byte(old)) {
		t.Fatal("source metadata mutated")
	}
	for _, bad := range [][]byte{nil, bytes.ReplaceAll(plist, []byte(old), []byte("unknown")), append(append([]byte{}, plist...), []byte(old)...)} {
		if _, err = synaraRuntimeInfoPlist(bad, old, next); err == nil {
			t.Fatal("unverified metadata accepted")
		}
	}
	sourceDigest := strings.Repeat("c", 64)
	hook := synaraRuntimeBackendHook("/Applications/Synara Beta.app/Contents/Resources/app.asar", "/private/tmp/owned/server.mjs", sourceDigest, next)
	for _, fragment := range []string{"if(url!==entry)return loaded", sourceDigest, next, "stat.isSymbolicLink()", "return {...loaded,source}"} {
		if !bytes.Contains(hook, []byte(fragment)) {
			t.Fatalf("scoped loader missing %q", fragment)
		}
	}
}

func TestSynaraPrivateRuntimePreservesActualPackageMetadata(t *testing.T) {
	for _, label := range []string{"1.0.0-beta.1", "1.0.1-beta.2", "9.7.0-beta.99", ""} {
		t.Run(label, func(t *testing.T) {
			metadata := map[string]any{"name": "synara-desktop-beta", "synaraDesktopFlavor": "beta", "main": "apps/desktop/new/main.js", "synaraCommitHash": "different-real-commit", "version": label, "futureMetadata": map[string]any{"capability": true}}
			source, _ := json.Marshal(metadata)
			data, mainEntry, err := synaraRuntimePackageData(source)
			if err != nil || mainEntry != metadata["main"] {
				t.Fatalf("actual Beta package rejected: %q %v", mainEntry, err)
			}
			var got map[string]any
			if json.Unmarshal(data, &got) != nil {
				t.Fatal("private package metadata is invalid")
			}
			metadata["main"] = "bootstrap.cjs"
			want, _ := json.Marshal(metadata)
			if !bytes.Equal(data, want) {
				t.Fatal("private runtime invented or discarded installation metadata")
			}
		})
	}
	for _, main := range []string{"", ".", "..", "../main.js", "/main.js", "apps/../main.js", `apps\main.js`, "apps/main.js\x00"} {
		source, _ := json.Marshal(map[string]any{"name": "synara-desktop-beta", "synaraDesktopFlavor": "beta", "main": main})
		if data, _, err := synaraRuntimePackageData(source); err == nil || data != nil {
			t.Fatalf("unsafe source entry accepted: %q", main)
		}
	}
}

func TestSynaraPrivateRuntimeBindsPreparedSourceDigests(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for private runtime digest guards")
	}
	root := t.TempDir()
	main := []byte("// arbitrary compatible Beta source\n" + synaraDesktopSpawnAnchor + "\n});\n")
	digest := openDesignHash(main)
	bootstrap := synaraRuntimeBootstrap(root, "main.js", "unused-hook", digest)
	// Exercise the emitted guard before Electron compilation using a small
	// Electron stub. No native application, provider or profile is opened.
	stub := `(()=>{const electron={app:{}};const Module=require('node:module');const originalLoad=Module._load;Module._load=(name,...args)=>name==='electron'?electron:originalLoad(name,...args);Module.prototype._compile=function(){globalThis.compiled=true};})();`
	path := filepath.Join(root, "bootstrap.cjs")
	if err := os.WriteFile(path, append(append([]byte(stub+"\n"), bootstrap...), []byte("if(!globalThis.compiled)throw Error('original desktop was not compiled');\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	for _, changed := range []bool{false, true} {
		data := main
		if changed {
			data = append(append([]byte{}, main...), []byte("// replacement after prepare\n")...)
		}
		if err := os.WriteFile(filepath.Join(root, "main.js"), data, 0600); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command(node, path).CombinedOutput()
		if !changed && err != nil || changed && (err == nil || !strings.Contains(string(output), "Synara desktop source changed")) {
			t.Fatalf("prepared desktop guard changed=%v: %v %s", changed, err, output)
		}
	}
	a := synaraRuntimeIdentity(root, digest, root, "main.js", digest, digest, digest)
	for _, identity := range [][]byte{
		synaraRuntimeIdentity(root, digest, root, "other.js", digest, digest, digest),
		synaraRuntimeIdentity(root, digest, root, "main.js", strings.Repeat("a", 64), digest, digest),
		synaraRuntimeIdentity(root, digest, root, "main.js", digest, strings.Repeat("b", 64), digest),
	} {
		if bytes.Equal(a, identity) {
			t.Fatal("prepared source change did not change the owned runtime identity")
		}
	}
}

func TestSynaraPrivateRuntimeBackendBindsPreparedSources(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for private backend digest guards")
	}
	if output, err := exec.Command(node, "-p", `typeof(require('node:module').registerHooks)`).CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "function" {
		t.Skip("this Node does not expose the synchronous loader hook")
	}
	root := t.TempDir()
	archive := filepath.Join(root, "source.asar")
	entry := filepath.Join(archive, "apps", "server", "dist", "index.mjs")
	if err := os.MkdirAll(filepath.Dir(entry), 0700); err != nil {
		t.Fatal(err)
	}
	// Node canonicalizes a normal on-disk entry URL. Use a canonical fixture
	// directory so this checks the source guards rather than a /var alias.
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	archive = filepath.Join(root, "source.asar")
	entry = filepath.Join(archive, "apps", "server", "dist", "index.mjs")
	private := filepath.Join(root, "server.mjs")
	original, patched := []byte("console.log('original');\n"), []byte("console.log('patched');\n")
	hook := filepath.Join(root, "backend-hook.mjs")
	if err := os.WriteFile(hook, synaraRuntimeBackendHook(archive, private, openDesignHash(original), openDesignHash(patched)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, errorText string
		original, patch []byte
	}{
		{name: "another compatible upstream digest", original: original, patch: patched},
		{name: "upstream replaced after prepare", original: append(append([]byte{}, original...), '\n'), patch: patched, errorText: "Synara server source changed"},
		{name: "private replaced after prepare", original: original, patch: append(append([]byte{}, patched...), '\n'), errorText: "Private Synara server changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(entry, test.original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(private, test.patch, 0600); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command(node, "--import", hook, entry).CombinedOutput()
			if test.errorText == "" && (err != nil || strings.TrimSpace(string(output)) != "patched") || test.errorText != "" && (err == nil || !strings.Contains(string(output), test.errorText)) {
				t.Fatalf("prepared backend guard: %v %s", err, output)
			}
		})
	}
}

func TestSynaraPrivateRuntimeRejectsResourceLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges differ on Windows")
	}
	installation := t.TempDir()
	outside := t.TempDir()
	for _, tc := range []struct{ name, target string }{{"absolute-internal", filepath.Join(installation, "file")}, {"external", filepath.Join(outside, "file")}} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(tc.target, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(installation, tc.name)
			if err := os.Symlink(tc.target, source); err != nil {
				t.Fatal(err)
			}
			if err := synaraCloneRuntimeItem(source, filepath.Join(installation, "must-not-copy"), installation); err == nil {
				t.Fatal("unsafe link accepted before clone")
			}
			if _, err := os.Lstat(filepath.Join(installation, "must-not-copy")); !os.IsNotExist(err) {
				t.Fatal("destination changed on rejected clone")
			}
			actual, err := os.ReadFile(tc.target)
			if err != nil || string(actual) != "original" {
				t.Fatal("original changed")
			}
		})
	}
}

func TestSynaraPrivateRuntimeSigningConfinedToOwnedBundle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink privileges differ on Windows")
	}
	parent := t.TempDir()
	bundle := filepath.Join(parent, "private.app")
	if err := os.MkdirAll(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "outside-code"), []byte("original code"), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(bundle, "must-not-sign")
	if err := os.Symlink("../outside-code", link); err != nil {
		t.Fatal(err)
	}
	if synaraOwnedRuntimeLinksSafe(bundle) == nil {
		t.Fatal("relative link outside owned bundle was accepted for deep signing")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "owned-code"), []byte("owned"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("owned-code", link); err != nil {
		t.Fatal(err)
	}
	if err := synaraOwnedRuntimeLinksSafe(bundle); err != nil {
		t.Fatal(err)
	}
	// A parent alias (macOS /var -> /private/var, or a temporary fixture alias)
	// must not turn a contained relative framework link into an apparent escape.
	alias := filepath.Join(t.TempDir(), "parent-alias")
	if err := os.Symlink(parent, alias); err != nil {
		t.Fatal(err)
	}
	if err := synaraOwnedRuntimeLinksSafe(filepath.Join(alias, filepath.Base(bundle))); err != nil {
		t.Fatal(err)
	}
	rootAlias := filepath.Join(parent, "bundle-root-alias")
	if err := os.Symlink(bundle, rootAlias); err != nil {
		t.Fatal(err)
	}
	if err := synaraOwnedRuntimeLinksSafe(rootAlias); err == nil {
		t.Fatal("bundle root symlink accepted")
	}
	actual, _ := os.ReadFile(filepath.Join(parent, "outside-code"))
	if string(actual) != "original code" {
		t.Fatal("outside code changed")
	}
}

func TestSynaraPrivateRuntimePreservesOtherPlatforms(t *testing.T) {
	for _, platform := range []string{"windows", "linux"} {
		t.Run(platform, func(t *testing.T) {
			options := synaraProfileTestOptions(t)
			saved, err := prepareSynaraPrivateRuntime(options, "unused native desktop", platform)
			if err != nil || saved.Revision != 0 || !synaraPrivateRuntimeReady(options, "unused native desktop", platform, saved) {
				t.Fatal("existing integration was not preserved")
			}
			saved.Revision = 1
			if synaraPrivateRuntimeReady(options, "unused native desktop", platform, saved) {
				t.Fatal("foreign private bootstrap accepted")
			}
		})
	}
}

func TestSynaraPrivateRuntimeInstalledPrepare(t *testing.T) {
	installed := os.Getenv("KILO_TEST_SYNARA_RUNTIME_APP")
	if installed == "" {
		t.Skip("set KILO_TEST_SYNARA_RUNTIME_APP for the owned runtime signing and source test")
	}
	if runtime.GOOS != "darwin" || !filepath.IsAbs(installed) {
		t.Fatal("installed private runtime validation requires a macOS bundle")
	}
	options := synaraProfileTestOptions(t)
	if err := os.MkdirAll(options.RootDir, 0700); err != nil {
		t.Fatal(err)
	}
	originals := map[string]string{}
	for _, relative := range []string{"Contents/MacOS/Synara Beta", "Contents/Info.plist", "Contents/_CodeSignature/CodeResources", "Contents/Frameworks/Electron Framework.framework/Versions/A/Electron Framework"} {
		path := filepath.Join(installed, filepath.FromSlash(relative))
		digest, err := openDesignFileHash(path, 512<<20)
		if err != nil {
			t.Fatal(err)
		}
		originals[path] = digest
	}
	saved, err := prepareSynaraPrivateRuntime(options, installed, "macos")
	if err != nil {
		t.Fatal(err)
	}
	if !synaraPrivateRuntimeReady(options, installed, "macos", saved) {
		t.Fatal("new runtime not ready")
	}
	packageSource, _, err := synaraReadASARSource(saved.SourceArchive, "package.json", 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	wantPackage, _, err := synaraRuntimePackageData(packageSource)
	gotPackage, _, gotErr := synaraReadASARSource(filepath.Join(saved.Root, synaraRuntimeBundleName, "Contents", "Resources", "app.asar"), "package.json", 64<<10)
	if err != nil || gotErr != nil || !bytes.Equal(wantPackage, gotPackage) {
		t.Fatal("owned runtime did not retain the installed Beta's real metadata")
	}
	for _, change := range []string{"replace-critical-file", "root", "executable", "header", "revision", "main-entry", "main-digest", "server-digest"} {
		t.Run(change, func(t *testing.T) {
			encoded, _ := json.Marshal(saved)
			var invalid synaraPrivateRuntime
			_ = json.Unmarshal(encoded, &invalid)
			switch change {
			case "replace-critical-file":
				delete(invalid.Files, synaraRuntimeHookName)
				invalid.Files["unrelated-file"] = strings.Repeat("a", 64)
			case "root":
				invalid.Root = filepath.Dir(invalid.Root)
			case "executable":
				invalid.Executable = filepath.Join(invalid.Root, "other-native-file")
			case "header":
				invalid.ArchiveHeaderSHA256 = strings.Repeat("a", 64)
			case "revision":
				invalid.Revision++
			case "main-entry":
				invalid.SourceMainEntry = "../main.js"
			case "main-digest":
				invalid.SourceMainSHA256 = strings.Repeat("a", 64)
			case "server-digest":
				invalid.SourceServerSHA256 = strings.Repeat("b", 64)
			}
			if synaraPrivateRuntimeReady(options, installed, "macos", invalid) {
				t.Fatal("changed private runtime identity remained ready")
			}
		})
	}
	if err = validateSynaraPrivateRuntimeLaunch(saved); err != nil {
		t.Fatal(err)
	}
	reused, err := prepareSynaraPrivateRuntime(options, installed, "macos")
	if err != nil || reused.Root != saved.Root {
		t.Fatal("same verified runtime was not reused")
	}
	for path, want := range originals {
		actual, err := openDesignFileHash(path, 512<<20)
		if err != nil || actual != want {
			t.Fatalf("original installation changed: %s", filepath.Base(path))
		}
	}
	if os.Getenv("KILO_TEST_SYNARA_RUNTIME_DESKTOP") == "1" {
		// The real Electron loads the transformed server under its original
		// archive URL. All four provider homes and its UI state are fixtures.
		saveSynaraProfilePlan(t, options)
		paths := synaraPaths(filepath.Dir(options.RootDir))
		if err := os.MkdirAll(paths.UIHome, 0700); err != nil {
			t.Fatal(err)
		}
		plan := clientLaunchPlan{Executable: saved.Executable, Args: []string{synaraMarker + options.RootDir}}
		overrides := map[string]string{"HOME": paths.UIHome, "USERPROFILE": paths.UIHome, "SYNARA_HOME": paths.Data, "SYNARA_BETA_HOME": paths.Data, "SYNARA_DESKTOP_SMOKE_USER_DATA": paths.Electron, "SYNARA_DISABLE_AUTO_UPDATE": "1", "XDG_CONFIG_HOME": filepath.Join(paths.UIHome, ".config"), "XDG_DATA_HOME": filepath.Join(paths.UIHome, ".local", "share"), "XDG_CACHE_HOME": filepath.Join(paths.UIHome, ".cache"), "XDG_STATE_HOME": filepath.Join(paths.UIHome, ".local", "state")}
		environment := map[string]string{}
		for _, entry := range clientChildEnvironment(os.Environ(), overrides, synaraUnsetEnvironment(os.Environ()), "macos") {
			name, value, ok := strings.Cut(entry, "=")
			if ok {
				environment[name] = value
			}
		}
		version, err := synaraVersion(installed, "macos")
		if err != nil {
			t.Fatal(err)
		}
		synaraInstalledDesktopStartup(t, plan, paths, environment, version)
	}
	manifest, err := os.ReadFile(filepath.Join(saved.Root, synaraRuntimeManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var public map[string]any
	if json.Unmarshal(manifest, &public) != nil || public["sourceApp"] != installed {
		t.Fatal("invalid public runtime source record")
	}
	hookPath := filepath.Join(saved.Root, synaraRuntimeHookName)
	if err = os.WriteFile(hookPath, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if synaraPrivateRuntimeReady(options, installed, "macos", saved) {
		t.Fatal("changed loader remained ready")
	}
	if _, err = prepareSynaraPrivateRuntime(options, installed, "macos"); err == nil {
		t.Fatal("modified existing cache was silently replaced")
	}
	actual, _ := os.ReadFile(hookPath)
	if string(actual) != "changed" {
		t.Fatal("changed cache was discarded")
	}
}
