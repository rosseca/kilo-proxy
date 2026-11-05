package main

import (
	"os"
	"strings"
	"testing"
)

func t3CodeOpenCodeKiloContractFixture() string {
	return `const kindOpenCode = ProviderDriverKind.make("opencode");
const mergeProviderInstanceEnvironment = Effect.fn(function* (environment, baseEnv = process.env) {
  if (!environment || environment.length === 0) return baseEnv;
  const next = { ...baseEnv };
  for (const variable of environment) next[variable.name] = variable.value;
  return next;
});
const OpenCodeDriver = {
  driverKind: kindOpenCode,
  metadata: { displayName: "OpenCode", supportsMultipleInstances: true },
  configSchema: OpenCodeSettings,
  defaultConfig: () => decode({}),
  create: ({ instanceId, environment, config }) => Effect.gen(function* () {
    const processEnv = yield* mergeProviderInstanceEnvironment(environment);
    const runtimeProbe = yield* probeOpenCodeRuntime(config, processEnv);
    const serverOwner = yield* OpenCodeServerOwner.make({ environment: processEnv });
    const openCode2Server = yield* OpenCode2Server.make({ environment: processEnv });
    return { instanceId, runtimeProbe, serverOwner, openCode2Server };
  })
};
const BUILT_IN_DRIVERS = [
  OpenCodeDriver,
];
`
}

func TestT3CodeOpenCodeKiloContractRequiresLiveInstanceAndEnvironment(t *testing.T) {
	source := t3CodeOpenCodeKiloContractFixture()
	if !t3CodeOpenCodeKiloSupported(source) {
		t.Fatal("complete native instance environment contract rejected")
	}
	for _, tc := range []struct{ name, old, next string }{
		{"registry", "const BUILT_IN_DRIVERS", "const UNUSED_DRIVERS"},
		{"registered-driver", "  OpenCodeDriver,", "  OtherDriver,"},
		{"driver-kind", `ProviderDriverKind.make("opencode")`, `ProviderDriverKind.make("other")`},
		{"schema", "configSchema: OpenCodeSettings", "configSchema: OtherSettings"},
		{"commented-schema", "configSchema: OpenCodeSettings,", "configSchema: OtherSettings, /* configSchema: OpenCodeSettings, defaultConfig:()=>decode({}), create: */"},
		{"single-instance", "supportsMultipleInstances: true", "supportsMultipleInstances: false"},
		{"absent-metadata", "supportsMultipleInstances: true", "unused: true"},
		{"quoted-metadata", "supportsMultipleInstances: true", `unused: "supportsMultipleInstances: true"`},
		{"commented-metadata", "supportsMultipleInstances: true", "/* supportsMultipleInstances: true */ unused: true"},
		{"factory-environment", "create: ({ instanceId, environment, config })", "create: ({ instanceId, config })"},
		{"instance-merge", "mergeProviderInstanceEnvironment(environment);", "mergeProviderInstanceEnvironment([]);"},
		{"probe-environment", "probeOpenCodeRuntime(config, processEnv)", "probeOpenCodeRuntime(config, process.env)"},
		{"v1-environment", "OpenCodeServerOwner.make({ environment: processEnv })", "OpenCodeServerOwner.make({ environment: process.env })"},
		{"v2-environment", "OpenCode2Server.make({ environment: processEnv })", "OpenCode2Server.make({ environment: process.env })"},
		{"merge-copy", "const next = { ...baseEnv }", "const next = {}"},
		{"merge-key", "next[variable.name] = variable.value", "next.CONSTANT = variable.value"},
		{"merge-value", "next[variable.name] = variable.value", `next[variable.name] = "variable.value"`},
		{"merge-result", "return next;", "return baseEnv;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(source, tc.old, tc.next, 1)
			if changed == source {
				t.Fatal("invalid negative fixture")
			}
			if t3CodeOpenCodeKiloSupported(changed) {
				t.Fatal("incomplete instance or environment contract accepted")
			}
		})
	}
	for _, changed := range []string{"", source + "\n" + source, "/*\n" + source + "\n*/", "`\n" + source + "\n`", strings.Replace(source, "const OpenCodeDriver = {", "const OpenCodeDriver = {\n  example: `supportsMultipleInstances: true }`,", 1)} {
		if changed != "" && strings.Contains(changed, "example:") {
			if !t3CodeOpenCodeKiloSupported(changed) {
				t.Fatal("quoted braces disturbed a complete driver contract")
			}
		} else if t3CodeOpenCodeKiloSupported(changed) {
			t.Fatal("absent or duplicated driver accepted")
		}
	}
}

func TestT3CodeOpenCodeKiloContractAcceptsBundledSymbolSuffixes(t *testing.T) {
	source := t3CodeOpenCodeKiloContractFixture()
	for _, replace := range [][2]string{{"kindOpenCode", "kind$17"}, {"mergeProviderInstanceEnvironment", "mergeProviderInstanceEnvironment$1"}, {"processEnv", "providerEnv$2"}, {"const next", "const copied$3"}, {"next[", "copied$3["}, {"return next", "return copied$3"}, {"Effect.fn", "fn$2"}, {"OpenCodeServerOwner.make", "make$23"}, {"OpenCode2Server.make", "make$24"}} {
		source = strings.ReplaceAll(source, replace[0], replace[1])
	}
	if !t3CodeOpenCodeKiloSupported(source) {
		t.Fatal("bundler symbol suffixes changed a valid instance contract")
	}
}

func TestT3CodeOpenCodeKiloContractAcceptsNormalizedMergeKey(t *testing.T) {
	source := t3CodeOpenCodeKiloContractFixture()
	normalized := strings.Replace(source, "for (const variable of environment) next[variable.name] = variable.value;", `for (const variable of environment) {
    if (typeof variable.value !== "string") continue;
    const name = windows ? variable.name.toUpperCase() : variable.name;
    if (windows) {
      for (const inherited of Object.keys(next)) if (inherited.toUpperCase() === name) delete next[inherited];
    }
    next[name] = name === "CODEX_HOME" || name === "CLAUDE_CONFIG_DIR" ? expandHomePath(variable.value, home) : variable.value;
  }`, 1)
	normalized = strings.Replace(normalized, "const next = { ...baseEnv };", `const home = yield* HomeDirectory;
  const windows = platform === "win32";
  const next = { ...baseEnv };`, 1)
	if normalized == source {
		t.Fatal("invalid fixture")
	}
	if !t3CodeOpenCodeKiloSupported(normalized) {
		t.Fatal("normalized environment key rejected")
	}
	for _, declaration := range []string{"let", "var"} {
		t.Run("declaration-"+declaration, func(t *testing.T) {
			changed := strings.Replace(normalized, "const name = windows ?", declaration+" name = windows ?", 1)
			if changed == normalized || !t3CodeOpenCodeKiloSupported(changed) {
				t.Fatal("normalized key declaration form rejected")
			}
		})
	}

	withCandidate := strings.Replace(normalized, "    const name = windows ?", `    next[inherited] = "ignored";
    const other = "CONSTANT";
    next[other] = "ignored";
    const name = windows ?`, 1)
	if withCandidate == normalized || !t3CodeOpenCodeKiloSupported(withCandidate) {
		t.Fatal("unrelated candidate key hid the live normalized key")
	}

	ordered := strings.Replace(normalized, `    const name = windows ? variable.name.toUpperCase() : variable.name;
    if (windows) {
      for (const inherited of Object.keys(next)) if (inherited.toUpperCase() === name) delete next[inherited];
    }
    next[name] = name === "CODEX_HOME" || name === "CLAUDE_CONFIG_DIR" ? expandHomePath(variable.value, home) : variable.value;`, `    if (windows) {
      for (const inherited of Object.keys(next)) if (inherited.toUpperCase() === name) delete next[inherited];
    }
    next[name] = name === "CODEX_HOME" || name === "CLAUDE_CONFIG_DIR" ? expandHomePath(variable.value, home) : variable.value;
    const name = windows ? variable.name.toUpperCase() : variable.name;`, 1)
	if ordered == normalized || t3CodeOpenCodeKiloSupported(ordered) {
		t.Fatal("forward-reference normalized key accepted")
	}

	for _, tc := range []struct{ name, old, next string }{
		{"constant-key", "const name = windows ? variable.name.toUpperCase() : variable.name;", `const name = "KILO_API_KEY";`},
		{"value-key", "const name = windows ? variable.name.toUpperCase() : variable.name;", "const name = variable.value;"},
		{"mixed-key", "const name = windows ? variable.name.toUpperCase() : variable.name;", "const name = windows ? variable.name : variable.value;"},
		{"return", "return next;", "return baseEnv;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(normalized, tc.old, tc.next, 1)
			if changed == normalized {
				t.Fatal("invalid negative fixture")
			}
			if t3CodeOpenCodeKiloSupported(changed) {
				t.Fatal("non-derived or incomplete normalized key accepted")
			}
		})
	}

	simple := strings.Replace(source, "for (const variable of environment) next[variable.name] = variable.value;", `for (const variable of environment) {
    const name = variable.name;
    next[name] = variable.value;
  }`, 1)
	if simple == source || !t3CodeOpenCodeKiloSupported(simple) {
		t.Fatal("simple normalized environment key rejected")
	}
	for _, tc := range []struct{ name, old, next string }{
		{"value", "next[name] = variable.value;", `next[name] = "fallback";`},
		{"return", "return next;", "return baseEnv;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := strings.Replace(simple, tc.old, tc.next, 1)
			if changed == simple {
				t.Fatal("invalid negative fixture")
			}
			if t3CodeOpenCodeKiloSupported(changed) {
				t.Fatal("incomplete normalized merge accepted")
			}
		})
	}
	for name, tc := range map[string]struct{ source, old, next string }{
		"normalized-longer-identifier": {simple, "next[name] = variable.value;", "mergednext[name] = variable.value;"},
		"direct-longer-identifier":     {source, "next[variable.name] = variable.value;", "mergednext[variable.name] = variable.value;"},
	} {
		t.Run(name, func(t *testing.T) {
			changed := strings.Replace(tc.source, tc.old, tc.next, 1)
			if changed == tc.source {
				t.Fatal("invalid negative fixture")
			}
			if t3CodeOpenCodeKiloSupported(changed) {
				t.Fatal("prefixed merge target accepted")
			}
		})
	}
}

// Opt-in package/source inspection only; it never launches T3 or its providers.
func TestT3CodeOpenCodeKiloInstalledSourceContract(t *testing.T) {
	path := os.Getenv("KILO_TEST_T3_SERVER_SOURCE")
	if path == "" {
		t.Skip("set an explicit official T3 server source path for source acceptance")
	}
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !t3CodeOpenCodeKiloSupported(string(source)) {
		t.Fatal("official server lacks the required OpenCode instance contract")
	}
}
