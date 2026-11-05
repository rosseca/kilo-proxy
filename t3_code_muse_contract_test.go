package main

import (
	"os"
	"strings"
	"testing"
)

func t3CodeMuseKiloContractFixture() string {
	return `const kindMuse = ProviderDriverKind.make("muse");
const mergeProviderInstanceEnvironment = Effect.fn(function* (environment, baseEnv = process.env) {
 const next = { ...baseEnv };
 for (const variable of environment) next[variable.name] = variable.value;
 return next;
});
const MuseDriver = {
 driverKind: kindMuse,
 metadata: { displayName: "Muse Code", supportsMultipleInstances: true },
 configSchema: MuseSettings,
 defaultConfig: () => decode({}),
 create: ({ instanceId, environment, config }) => Effect.gen(function* () {
  const processEnvironment = yield* mergeProviderInstanceEnvironment(environment, makeMuseEnvironment(hostEnvironment));
  const snapshot = yield* checkMuseProviderStatus(config, processEnvironment, cwd);
  const orchestrationAdapter = yield* makeMuseAdapterV2({ environment: processEnvironment });
  const textGeneration = yield* makeMuseTextGeneration(config, { environment: processEnvironment });
  return { instanceId, snapshot, orchestrationAdapter, textGeneration };
 })
};
const BUILT_IN_DRIVERS = [
 MuseDriver,
];
`
}

func TestT3CodeMuseKiloContractRequiresLiveScopedEnvironment(t *testing.T) {
	source := t3CodeMuseKiloContractFixture()
	if !t3CodeMuseKiloSupported(source) {
		t.Fatal("complete native Muse instance contract rejected")
	}
	for _, tc := range []struct{ name, old, next string }{
		{"registry", "const BUILT_IN_DRIVERS", "const UNUSED_DRIVERS"},
		{"registered-driver", " MuseDriver,", " OtherDriver,"},
		{"kind", `ProviderDriverKind.make("muse")`, `ProviderDriverKind.make("other")`},
		{"schema", "configSchema: MuseSettings", "configSchema: OtherSettings"},
		{"commented-schema", "configSchema: MuseSettings,", "configSchema: OtherSettings, /* configSchema: MuseSettings, defaultConfig:()=>decode({}), create: */"},
		{"multiple", "supportsMultipleInstances: true", "supportsMultipleInstances: false"},
		{"quoted-metadata", "supportsMultipleInstances: true", `unused: "supportsMultipleInstances: true"`},
		{"commented-metadata", "supportsMultipleInstances: true", "/*supportsMultipleInstances: true*/unused:true"},
		{"factory", "create: ({ instanceId, environment, config })", "create: ({ instanceId, config })"},
		{"merge-input", "mergeProviderInstanceEnvironment(environment, makeMuseEnvironment(hostEnvironment))", "mergeProviderInstanceEnvironment([], makeMuseEnvironment(hostEnvironment))"},
		{"merge-base", "makeMuseEnvironment(hostEnvironment)", "hostEnvironment"},
		{"status", "checkMuseProviderStatus(config, processEnvironment, cwd)", "checkMuseProviderStatus(config, process.env, cwd)"},
		{"adapter", "makeMuseAdapterV2({ environment: processEnvironment })", "makeMuseAdapterV2({ environment: process.env })"},
		{"generation", "makeMuseTextGeneration(config, { environment: processEnvironment })", "makeMuseTextGeneration(config, { environment: process.env })"},
		{"merge-copy", "const next = { ...baseEnv }", "const next = {}"},
		{"merge-key", "next[variable.name] = variable.value", "next.CONSTANT = variable.value"},
		{"merge-value", "next[variable.name] = variable.value", `next[variable.name] = "variable.value"`},
		{"merge-value-distractor", "next[variable.name] = variable.value", `next[variable.name] = "fallback"; const unused = variable.value`},
		{"merge-return", "return next;", "return baseEnv;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(source, tc.old, tc.next, 1)
			if changed == source {
				t.Fatal("invalid negative fixture")
			}
			if t3CodeMuseKiloSupported(changed) {
				t.Fatal("incomplete scoped Muse contract accepted")
			}
		})
	}
	for _, invalid := range []string{"", source + source, "/*\n" + source + "\n*/", "`\n" + source + "\n`"} {
		if t3CodeMuseKiloSupported(invalid) {
			t.Fatal("absent, duplicated or non-live driver accepted")
		}
	}
	conditional := strings.Replace(source, "next[variable.name] = variable.value", `next[variable.name] = variable.name === "CODEX_HOME" || variable.name === "CLAUDE_CONFIG_DIR" ? expandHomePath(variable.value, home) : variable.value`, 1)
	if !t3CodeMuseKiloSupported(conditional) {
		t.Fatal("current home-expanding merger rejected")
	}
	for _, invalid := range []string{strings.Replace(conditional, ": variable.value", `: "fallback"`, 1), strings.Replace(conditional, `"CLAUDE_CONFIG_DIR"`, `"META_API_KEY"`, 1)} {
		if t3CodeMuseKiloSupported(invalid) {
			t.Fatal("conditional merger changed an instance credential")
		}
	}
	source = strings.ReplaceAll(source, "mergeProviderInstanceEnvironment", "mergeProviderInstanceEnvironment$2")
	source = strings.ReplaceAll(source, "Effect.fn", "fn$3")
	source = strings.ReplaceAll(source, "processEnvironment", "providerEnv$4")
	if !t3CodeMuseKiloSupported(source) {
		t.Fatal("compiled symbol suffixes rejected")
	}
}

func TestT3CodeMuseKiloContractAcceptsNormalizedMergeKey(t *testing.T) {
	source := t3CodeMuseKiloContractFixture()
	normalized := strings.Replace(source, "for (const variable of environment) next[variable.name] = variable.value;", `for (const variable of environment) {
    const windows = platform === "win32";
    const name = windows ? variable.name.toUpperCase() : variable.name;
    if (windows) {
      for (const inherited of Object.keys(next)) if (inherited.toUpperCase() === name) delete next[inherited];
    }
    next[name] = name === "CODEX_HOME" || name === "CLAUDE_CONFIG_DIR" ? expandHomePath(variable.value, home) : variable.value;
  }`, 1)
	if normalized == source {
		t.Fatal("invalid fixture")
	}
	if !t3CodeMuseKiloSupported(normalized) {
		t.Fatal("normalized environment key rejected")
	}
	for _, tc := range []struct{ name, old, next string }{
		{"constant-key", "const name = windows ? variable.name.toUpperCase() : variable.name;", `const name = "MUSE_API_KEY";`},
		{"value-key", "const name = windows ? variable.name.toUpperCase() : variable.name;", "const name = variable.value;"},
		{"value", ": variable.value;", `: "fallback";`},
		{"env-name", `"CLAUDE_CONFIG_DIR"`, `"META_API_KEY"`},
		{"return", "return next;", "return baseEnv;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(normalized, tc.old, tc.next, 1)
			if changed == normalized {
				t.Fatal("invalid negative fixture")
			}
			if t3CodeMuseKiloSupported(changed) {
				t.Fatal("non-derived or incomplete normalized merge accepted")
			}
		})
	}
}

func TestT3CodeMuseKiloInstalledSourceContract(t *testing.T) {
	sourcePath := os.Getenv("KILO_TEST_T3_SERVER_SOURCE")
	if sourcePath == "" {
		t.Skip("set an explicit official T3 server source path for source acceptance")
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !t3CodeMuseKiloSupported(string(source)) {
		t.Fatal("official compiled Muse instance contract rejected")
	}
}
