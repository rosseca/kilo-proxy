package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestT3CodeKiloContractAcceptsTracedDriverFactories(t *testing.T) {
	for _, provider := range []struct {
		name    string
		source  string
		factory string
		check   func(string) bool
	}{
		{"OpenCode", t3CodeOpenCodeKiloContractFixture(), "create: ({ instanceId, environment, config }) => Effect.gen(function* () {", t3CodeOpenCodeKiloSupported},
		{"Muse", t3CodeMuseKiloContractFixture(), "create: ({ instanceId, environment, config }) => Effect.gen(function* () {", t3CodeMuseKiloSupported},
	} {
		t.Run(provider.name, func(t *testing.T) {
			for _, wrapper := range []string{"Effect.fn", "fn", "fn$2"} {
				t.Run(wrapper, func(t *testing.T) {
					factory := "create: " + wrapper + "(\"" + provider.name + "Driver.create\")(function* ({ instanceId, environment, config }) {"
					traced := strings.Replace(provider.source, provider.factory, factory, 1)
					if traced == provider.source {
						t.Fatal("invalid traced factory fixture")
					}
					if !provider.check(traced) {
						t.Fatal("complete traced native instance contract rejected")
					}
					for _, change := range []struct{ name, old, next string }{
						{"factory-environment", "function* ({ instanceId, environment, config })", "function* ({ instanceId, config })"},
						{"aliased-environment", "function* ({ instanceId, environment, config })", "function* ({ instanceId, environment: inherited, config })"},
						{"rest-environment", "function* ({ instanceId, environment, config })", "function* ({ instanceId, ...environment })"},
						{"factory-generator", "\")(function* ({", "\")(function ({"},
						{"factory-wrapper", wrapper + "(\"", "unknownWrapper(\""},
						{"multiple-instances", "supportsMultipleInstances: true", "supportsMultipleInstances: false"},
						{"commented-schema", "configSchema: " + provider.name + "Settings,", "configSchema: OtherSettings, /* configSchema: " + provider.name + "Settings, defaultConfig:()=>decode({}), create: */"},
						{"instance-merge", "mergeProviderInstanceEnvironment(environment", "mergeProviderInstanceEnvironment([]"},
						{"copied-environment", "const next = { ...baseEnv }", "const next = {}"},
						{"duplicate-factory", "defaultConfig:", "create: ({ environment }) => environment,\n defaultConfig:"},
					} {
						t.Run(change.name, func(t *testing.T) {
							changed := strings.Replace(traced, change.old, change.next, 1)
							if changed == traced {
								t.Fatal("invalid negative traced fixture")
							}
							if provider.check(changed) {
								t.Fatal("incomplete traced instance contract accepted")
							}
						})
					}
					for _, nonLive := range []string{"/*\n" + traced + "\n*/", "`\n" + traced + "\n`"} {
						if provider.check(nonLive) {
							t.Fatal("commented or quoted traced contract accepted")
						}
					}
				})
			}
			if !provider.check(provider.source) {
				t.Fatal("legacy arrow factory rejected")
			}
		})
	}
}

// This opt-in check reads an explicit desktop package without launching T3.
func TestT3CodeKiloInstalledPackageContract(t *testing.T) {
	executable := os.Getenv("KILO_TEST_T3_PACKAGE")
	if executable == "" {
		t.Skip("set an explicit official T3 desktop path for package acceptance")
	}
	if !filepath.IsAbs(executable) {
		t.Fatal("official T3 desktop path must be absolute")
	}
	installation, err := inspectT3CodeInstallation(executable, runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	if !installation.OpenCodeKiloSupported || !installation.MuseKiloSupported {
		t.Fatalf("official T3 package lacks scoped accounts: OpenCode=%v Muse=%v", installation.OpenCodeKiloSupported, installation.MuseKiloSupported)
	}
}

func TestT3CodeKiloContractAcceptsScopedAppImageBaseEnvironment(t *testing.T) {
	// Compiled from T3's AppImage environment sanitizer. Keep the inherited
	// environment cleanup separate from the provider's instance overrides.
	const upstream = `const APPIMAGE_RUNTIME_ENV_KEYS = [
"APPIMAGE",
"APPDIR",
"ARGV0",
"OWD"
];
const APPIMAGE_PATH_LIKE_ENV_SEPARATORS = {
PATH: /:/,
LD_LIBRARY_PATH: /[:;]/,
XDG_DATA_DIRS: /:/,
GSETTINGS_SCHEMA_DIR: /:/
};
function isPathSegmentUnderAppDir(segment, appDir) {
	return segment === appDir || segment.startsWith(` + "`${appDir}/`" + `);
}
function stripAppImageRuntimeEnv(env) {
	if (env.APPIMAGE === void 0 && env.APPDIR === void 0) return env;
	const scrubbed = { ...env };
	for (const key of APPIMAGE_RUNTIME_ENV_KEYS) delete scrubbed[key];
	const appDir = env.APPDIR?.replace(/\/+$/, "");
	if (appDir) for (const [key, separator] of Object.entries(APPIMAGE_PATH_LIKE_ENV_SEPARATORS)) {
		const value = scrubbed[key];
		if (value === void 0) continue;
		const segments = value.split(separator);
		const kept = segments.filter((segment) => !isPathSegmentUnderAppDir(segment, appDir));
		if (kept.length === segments.length) continue;
		if (kept.length === 0) delete scrubbed[key];
		else scrubbed[key] = kept.length === 1 && kept[0] === "" ? ":" : kept.join(":");
	}
	return scrubbed;
}
`
	for _, tc := range []struct {
		name   string
		source string
		check  func(string) bool
	}{
		{"OpenCode", t3CodeOpenCodeKiloContractFixture(), t3CodeOpenCodeKiloSupported},
		{"Muse", t3CodeMuseKiloContractFixture(), t3CodeMuseKiloSupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const start = "function* (environment, baseEnv = process.env) {"
			instance := strings.Replace(tc.source, start, start+"\n const hostEnv = stripAppImageRuntimeEnv(baseEnv);", 1)
			instance = strings.Replace(instance, "const next = { ...baseEnv };", "const next = { ...hostEnv };", 1)
			source := upstream + instance
			if !tc.check(source) {
				t.Fatal("complete AppImage-scoped merge contract rejected")
			}
			for _, alteration := range []struct{ name, from, to string }{
				{"absent-scrubber", upstream, ""},
				{"commented-scrubber", upstream, "/*\n" + upstream + "\n*/"},
				{"unverified-scrubber", "stripAppImageRuntimeEnv(baseEnv)", "arbitraryEnvironment(baseEnv)"},
				{"different-base", "stripAppImageRuntimeEnv(baseEnv)", "stripAppImageRuntimeEnv(process.env)"},
				{"unbound-host", "const hostEnv = stripAppImageRuntimeEnv(baseEnv);", "if (false) { const hostEnv = stripAppImageRuntimeEnv(baseEnv); }"},
				{"ambient-mutation", "delete scrubbed[key];", "delete env[key];"},
				{"changed-scrubber-key", `"OWD"`, `"OPENCODE_CONFIG_DIR"`},
				{"mutable-scrubber-keys", upstream, upstream + "APPIMAGE_RUNTIME_ENV_KEYS.push(externalKey);\n"},
				{"duplicate-scrubber", upstream, upstream + upstream},
				{"discarded-instance-value", "next[variable.name] = variable.value;", "next[variable.name] = hostEnv[variable.name];"},
				{"discarded-instance-result", "return next;", "return hostEnv;"},
			} {
				t.Run(alteration.name, func(t *testing.T) {
					invalid := strings.Replace(source, alteration.from, alteration.to, 1)
					if invalid == source || tc.check(invalid) {
						t.Fatal("invalid AppImage-scoped instance environment accepted")
					}
				})
			}
		})
	}
}

func TestT3CodeKiloContractAcceptsScopedHomeVariableHelper(t *testing.T) {
	const helper = `const HOME_PATH_VARIABLES = /* @__PURE__ */ new Set(["CODEX_HOME", "CLAUDE_CONFIG_DIR", "GROK_HOME"]);
function expandHomeVariable$1(name, value, home) {
	return HOME_PATH_VARIABLES.has(name) ? expandHomePath(value, home) : value;
}
`
	for _, tc := range []struct {
		name      string
		source    string
		supported func(string) bool
	}{
		{"OpenCode", t3CodeOpenCodeKiloContractFixture(), t3CodeOpenCodeKiloSupported},
		{"Muse", t3CodeMuseKiloContractFixture(), t3CodeMuseKiloSupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const original = "for (const variable of environment) next[variable.name] = variable.value;"
			const scoped = `const home = yield* HomeDirectory;
	const windows = (yield* Platform) === "win32";
	for (const variable of environment) {
		const name = windows ? variable.name.toUpperCase() : variable.name;
		next[name] = expandHomeVariable$1(name, variable.value, home);
	}`
			// An odd number of quotes in a regular expression must not hide
			// the subsequent live declarations from the contract masker.
			const marker = `const bundleMarker = /"product-name"\s*=\s*<"([^"]+)">/;` + "\n"
			source := marker + helper + strings.Replace(tc.source, original, scoped, 1)
			if !tc.supported(source) {
				t.Fatal("verified home expansion helper rejected")
			}
			for _, change := range []struct{ name, from, to string }{
				{"unbound helper", "expandHomeVariable$1(name, variable.value, home)", "expandHomeVariable(name, variable.value, home)"},
				{"different key", "expandHomeVariable$1(name, variable.value, home)", `expandHomeVariable$1("HOME", variable.value, home)`},
				{"different value", "expandHomeVariable$1(name, variable.value, home)", "expandHomeVariable$1(name, variable.name, home)"},
				{"different allowlist", `"GROK_HOME"`, `"OPENCODE_CONFIG_DIR"`},
				{"mutable allowlist", helper, helper + "HOME_PATH_VARIABLES.add(externalKey);\n"},
				{"different fallback", " : value;", " : home;"},
				{"commented definition", helper, "// " + strings.ReplaceAll(helper, "\n", "\n// ") + "\n"},
			} {
				t.Run(change.name, func(t *testing.T) {
					changed := strings.Replace(source, change.from, change.to, 1)
					if changed == source || tc.supported(changed) {
						t.Fatal("invalid or accepted non-forwarding fixture")
					}
				})
			}
		})
	}
}

func TestT3CodeOpenCodeKiloContractAcceptsScopedRuntimeProbe(t *testing.T) {
	const helper = `function providerProbeEnvironment$1(environment) {
	return { ...environment, MISE_OFFLINE: "1" };
}
`
	const original = "const runtimeProbe = yield* probeOpenCodeRuntime(config, processEnv);"
	const scoped = `const probeRuntime = (probeEnv) => probeOpenCodeRuntime(effectiveConfig, probeEnv).pipe(provideService(HttpClient, httpClient));
	const runtimeProbe = yield* makeOpenCodeRuntimeProbe(probeRuntime(providerProbeEnvironment$1(processEnv)));
	const resolveRuntime = runtimeProbe.lastSuccess.pipe(flatMap(match({
		onNone: () => probeRuntime(processEnv).pipe(tap(runtimeProbe.record))
	})));`
	source := helper + strings.Replace(t3CodeOpenCodeKiloContractFixture(), original, scoped, 1)
	if !t3CodeOpenCodeKiloSupported(source) {
		t.Fatal("scoped status probe and fallback rejected")
	}
	for _, change := range []struct{ name, from, to string }{
		{"unbound probe helper", "providerProbeEnvironment$1(processEnv)", "providerProbeEnvironment(processEnv)"},
		{"background environment", "providerProbeEnvironment$1(processEnv)", "providerProbeEnvironment$1(process.env)"},
		{"fallback environment", "() => probeRuntime(processEnv)", "() => probeRuntime(process.env)"},
		{"inner probe environment", "probeOpenCodeRuntime(effectiveConfig, probeEnv)", "probeOpenCodeRuntime(effectiveConfig, process.env)"},
		{"environment discarded", "...environment,", "...process.env,"},
		{"key overwritten", `MISE_OFFLINE: "1"`, `KILO_API_KEY: "1"`},
		{"commented definition", helper, "/*\n" + helper + "*/\n"},
	} {
		t.Run(change.name, func(t *testing.T) {
			changed := strings.Replace(source, change.from, change.to, 1)
			if changed == source || t3CodeOpenCodeKiloSupported(changed) {
				t.Fatal("invalid or accepted non-scoped probe fixture")
			}
		})
	}
}

func TestT3CodeContractMaskKeepsContinuedStringsHidden(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n", "\r"} {
		source := "const quoted = 'continued\\" + ending + "hidden';" + ending + "const live = true;"
		code := t3CodeContractCode(source)
		if len(code) != len(source) || strings.Contains(code, "hidden") || !strings.Contains(code, "const live = true;") {
			t.Fatalf("invalid masking with line ending %q", ending)
		}
	}
}
