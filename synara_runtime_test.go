package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSynaraPrivateRuntimeBootstrapArchive(t *testing.T) {
	packageData := []byte(`{"name":"synara-desktop-beta","main":"bootstrap.cjs"}`)
	bootstrap := synaraRuntimeBootstrap("/Applications/Synara Beta.app/Contents/Resources/app.asar", "/private/tmp/owned/backend-hook.mjs")
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
	for _, fragment := range []string{`app.getAppPath=()=>originalRoot`, synaraDesktopMainSHA256, `\"--import\"`, `originalModule._compile(source,originalMain)`} {
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
	hook := synaraRuntimeBackendHook("/Applications/Synara Beta.app/Contents/Resources/app.asar", "/private/tmp/owned/server.mjs", next)
	for _, fragment := range []string{"if(url!==entry)return loaded", synaraDelegationServerSHA256, next, "stat.isSymbolicLink()", "return {...loaded,source}"} {
		if !bytes.Contains(hook, []byte(fragment)) {
			t.Fatalf("scoped loader missing %q", fragment)
		}
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
	for _, change := range []string{"replace-critical-file", "root", "executable", "header", "revision"} {
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
		synaraInstalledDesktopStartup(t, plan, paths, environment)
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
