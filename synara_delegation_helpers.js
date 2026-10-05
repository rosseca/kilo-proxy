// Loaded only in Kilo Proxy's private, version-checked Synara server snapshot.
const kiloSynaraGatewayModels = __KILO_SYNARA_GATEWAY_MODELS__;
function kiloSynaraDefaultModel(input) {
	const managed = input.instanceId === "kilo_codex_proxy" ? kiloSynaraGatewayModels.codex : input.instanceId === "kilo_claude_proxy" ? kiloSynaraGatewayModels.claudeAgent : null;
	if (managed) {
		const configured = kiloSynaraGatewayModels.defaultModel;
		return input.availability?.customModels?.includes(configured) && managed.some(value => value.slug === configured) ? configured : null;
	}
	return providerDefaultModel(input.provider);
}
function kiloSynaraAccountAvailabilities(settings, statuses) {
	const instances = deriveProviderInstances(settings);
	const result = new Map();
	for (const instance of instances) {
		const status = statuses.find(value => value.instanceId === instance.instanceId && (value.driver ?? value.provider) === instance.driver);
		const customModels = Array.isArray(instance.config.customModels) ? instance.config.customModels.filter(value => typeof value === "string" && value.trim().length > 0) : [];
		result.set(instance.instanceId, {
			provider: instance.driver, instanceId: instance.instanceId, displayName: instance.displayName,
			enabled: settings.providers[instance.driver].enabled && instance.enabled,
			available: status?.available === true,
			...(status?.authStatus ? { authStatus: status.authStatus } : {}),
			...(status?.message ? { message: status.message } : {}),
			customModels,
			accountRequired: instance.isDefault && instances.some(value => value.driver === instance.driver && !value.isDefault),
			isDefault: instance.isDefault
		});
	}
	return result;
}
function kiloSynaraGatewayAccount(input) {
	const target = input.target;
	const account = input.availability;
	if (!target.instanceId && account?.accountRequired) throw new AgentGatewayTargetError("account_required", `Choose an exact ${target.provider} account from synara_capabilities providers[].instanceId and include target.instanceId. Normal and Kilo Proxy accounts are separate.`);
	if (target.instanceId && (!account || account.instanceId !== target.instanceId)) throw new AgentGatewayTargetError("account_unavailable", `Account "${target.instanceId}" is not configured. Choose an exact account from synara_capabilities.`);
	if (account?.provider && account.provider !== target.provider) throw new AgentGatewayTargetError("account_provider_mismatch", `Account "${account.instanceId}" belongs to ${account.provider}, not ${target.provider}.`);
	// The ordinary account preflight supplies its disabled/auth message first.
	if (account?.enabled === false || account?.available === false || account?.authStatus === "unauthenticated") return target;
	const managed = target.instanceId === "kilo_claude_proxy" ? kiloSynaraGatewayModels.claudeAgent.find(value => value.slug === target.model) : null;
	if (managed && target.options?.effort !== undefined && target.options.effort !== managed.kiloSavedEffort) throw new AgentGatewayTargetError("model_option_unavailable", `Claude · Kilo Proxy uses the effort saved for "${target.model}" in Kilo Proxy Models. Close and reopen its workspace after changing that default. Do not substitute a different account or model.`);
	return target;
}
function kiloSynaraMergeAccountModels(input, discovered) {
	const declared = input.availability?.customModels ?? [];
	const managed = input.instanceId === "kilo_codex_proxy" ? kiloSynaraGatewayModels.codex : input.instanceId === "kilo_claude_proxy" ? kiloSynaraGatewayModels.claudeAgent : [];
	// Kilo account choices are the exact prepared gateway IDs. Native aliases
	// remain confined to the normal account's discovery catalog.
	if (managed.length > 0) return declared.flatMap(slug => {
		const descriptor = managed.find(value => value.slug === slug);
		return descriptor ? [descriptor] : [];
	});
	const bySlug = new Map(discovered.map(value => [value.slug, value]));
	for (const slug of declared) {
		const descriptor = managed.find(value => value.slug === slug);
		if (descriptor) bySlug.set(slug, descriptor);
		else if (!bySlug.has(slug)) bySlug.set(slug, { slug, name: slug });
	}
	return [...bySlug.values()];
}
function kiloSynaraGatewayCatalogs(availabilities, discovery, cwd) {
	const accounts = [...availabilities.values()].filter(value => !value.isDefault || !value.accountRequired);
	return Effect.forEach(accounts, account => loadAgentGatewayProviderCatalog({ provider: account.provider, instanceId: account.instanceId, availability: account, discovery, cwd }));
}
