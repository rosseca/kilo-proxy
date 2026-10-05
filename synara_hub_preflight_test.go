package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exact official Beta 1.0.0-beta.1 closures. The opt-in source check below
// compares them with the SHA-pinned installed server before executing them
// with synthetic dependencies; it never creates real threads or Hub tasks.
const synaraHubSubmitContract = `	const submit = (input, context) => Effect.gen(function* () {
		if (!isServerGroupsEnabled()) return null;
		const principal = yield* principalFor(context);
		if (principal.kind !== "coordinator") return null;
		yield* context.assertCallerTurnActive();
		const sourcesByTask = yield* Effect.forEach(input.threads, (spec) => resolveHubWorkSource({
			snapshotQuery,
			...dependencies.projectionTurns ? { projectionTurns: dependencies.projectionTurns } : {},
			callerThreadId: context.callerThreadId,
			callerTurnId: context.callerTurnId,
			...spec.contextMessageIds !== void 0 ? { contextMessageIds: spec.contextMessageIds } : {}
		}));
		const sourceMessages = [...new Map(sourcesByTask.flat().map((message) => [message.messageId, message])).values()];
		const caller = yield* snapshotQuery.getThreadShellById(ThreadId.makeUnsafe(context.callerThreadId));
		if (Option.isNone(caller)) return yield* Effect.fail(new ToolInputError("Coordinator thread was not found."));
		const inheritedRuntimeMode = caller.value.runtimeMode === "auto" ? "approval-required" : caller.value.runtimeMode;
		const tasks = [];
		for (const [taskIndex, spec] of input.threads.entries()) {
			const project = yield* snapshotQuery.getProjectShellById(spec.projectId ?? principal.projectId);
			if (Option.isNone(project)) return yield* Effect.fail(new ToolInputError("Target project was not found."));
			const repositoryContext = spec.environment === void 0 && project.value.kind === "project" ? yield* dependencies.git.readBranchContext(project.value.workspaceRoot) : null;
			const environment = spec.environment ?? (repositoryContext?.isRepo ? "worktree" : "local");
			tasks.push({ spec: {
				...spec,
				environment,
				runtimeMode: spec.runtimeMode ?? inheritedRuntimeMode,
				contextMessageIds: sourcesByTask[taskIndex].map((message) => message.messageId)
			} });
		}
		const result = yield* service.submit({
			callerThreadId: ThreadId.makeUnsafe(context.callerThreadId),
			...context.callerTurnId ? { callerTurnId: TurnId.makeUnsafe(context.callerTurnId) } : {},
			requestId: input.requestId,
			sourceMessages,
			tasks
		});
		return mcpToolResultJson$1({
			requestId: input.requestId,
			replayed: result.replayed,
			acceptedCount: result.items.length,
			status: "accepted",
			workItems: result.items.map(hubWorkItem)
		});
	}).pipe(Effect.catch((error) => Effect.succeed(mcpToolResultError(errorText$1(error)))));`

const synaraHubResolveTargetContract = `	const resolveAutomationTarget = (input) => Effect.gen(function* () {
		const project = yield* snapshotQuery.getProjectShellById(input.projectId).pipe(Effect.mapError((error) => new ToolInputError(errorText$1(error))), Effect.flatMap(Option.match({
			onNone: () => Effect.fail(new ToolInputError(` + "`" + `Project "${input.projectId}" was not found.` + "`" + `)),
			onSome: Effect.succeed
		})));
		const availability = (yield* loadProviderAvailabilities).get(input.target.provider);
		return yield* resolveAgentGatewayTarget({
			target: input.target,
			discovery: providerDiscovery,
			...availability !== void 0 ? { availability } : {},
			cwd: project.workspaceRoot
		});
	});`

func TestSynaraDelegationHubPreflightsEveryAccountBeforeQueue(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the Synara Hub preflight contract")
	}
	options := synaraProfileTestOptions(t)
	options.Library = modelLibrary{SchemaVersion: 1, DefaultModel: "chatgpt/gpt-6-astra", Models: []modelLibraryItem{
		{ID: "chatgpt/gpt-6-astra", DisplayName: "GPT-6 Astra", ReasoningCustom: true, ReasoningEffort: "high", ReasoningLevels: []string{"low", "high"}},
		{ID: "anthropic/claude-opus-5", DisplayName: "Claude Opus 5", ReasoningCustom: true, ReasoningEffort: "high", ReasoningLevels: []string{"low", "high"}},
	}}
	models, err := synaraDelegationModels(options)
	if err != nil {
		t.Fatal(err)
	}
	var patches []synaraDelegationPatch
	if err := json.Unmarshal(synaraDelegationPatchesJSON, &patches); err != nil {
		t.Fatal(err)
	}
	fixture := map[string]any{"models": models, "helpers": synaraDelegationHelpers, "originalHubSubmit": synaraHubSubmitContract}
	submit, resolveTarget := synaraHubSubmitContract, synaraHubResolveTargetContract
	wired, guarded := false, false
	for _, patch := range patches {
		for key, prefix := range map[string]string{
			"catalog": "function loadAgentGatewayProviderCatalog(input) {",
			"resolve": "function resolveAgentGatewayTarget(input) {",
		} {
			if strings.HasPrefix(patch.After, prefix) {
				fixture[key] = patch.After
			}
		}
		if strings.Contains(patch.Before, "createThreads: runCreateThreads") && strings.Contains(patch.After, "validateTarget: resolveAutomationTarget") {
			wired = true
		}
		if strings.Contains(submit, patch.Before) {
			if strings.Count(submit, patch.Before) != patch.Count {
				t.Fatal("ambiguous Hub preflight contract")
			}
			submit = strings.ReplaceAll(submit, patch.Before, patch.After)
			guarded = strings.Contains(submit, "dependencies.validateTarget")
		}
		if strings.Contains(resolveTarget, patch.Before) {
			resolveTarget = strings.ReplaceAll(resolveTarget, patch.Before, patch.After)
		}
	}
	if !wired || !guarded {
		t.Fatal("Hub target resolver was not connected before queue admission")
	}
	preflight := strings.Index(submit, "dependencies.validateTarget")
	if git := strings.Index(submit, "dependencies.git.readBranchContext"); preflight < 0 || git <= preflight {
		t.Fatal("Hub preflight must precede worktree inspection")
	}
	if queue := strings.Index(submit, "service.submit("); queue <= preflight {
		t.Fatal("Hub preflight must precede durable queue admission")
	}
	fixture["hubSubmit"], fixture["resolveTarget"] = submit, resolveTarget
	data, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "hub-preflight.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(node, "scripts/verify_synara_hub_preflight.mjs", path).CombinedOutput()
	if err != nil {
		t.Fatalf("Synara Hub account preflight: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
}

func TestSynaraDelegationHubInstalledContract(t *testing.T) {
	path := os.Getenv("KILO_TEST_SYNARA_SERVER_SOURCE")
	if path == "" {
		t.Skip("set KILO_TEST_SYNARA_SERVER_SOURCE to the extracted official beta server")
	}
	source, err := readOpenDesignShimFile(path, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range []string{synaraHubSubmitContract, synaraHubResolveTargetContract} {
		if strings.Count(string(source), contract) != 1 {
			t.Fatal("official Hub preflight source contract changed")
		}
	}
	if _, err := patchSynaraDelegationServer(source, synaraProfileTestOptions(t)); err != nil {
		t.Fatal(err)
	}
}
