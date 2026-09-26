# Model pack candidates and evidence

These five built-in packs are **candidate starting points**, reviewed on September 26, 2026. They are not a ranking of the best models for each profession. Their exact gateway IDs and optional models are in [`model-packs.json`](../model-packs.json); the native UI shows each model's current price and availability from the connected team's catalog, not this document. An ID in the catalog and a `tools` flag do not establish that every editor protocol or paid inference request works. Assignments are explicit and are checked against the current team's catalog before agent preparation.

## How candidates were chosen

1. Require exact IDs that appear in a Kilo team catalog, a supported context window, and declared capabilities appropriate to the role. A different team may see unavailable entries; it can use a personal pack instead.
2. Treat [Kilo's public model leaderboard](https://kilo.ai/leaderboard) and [model statistics](https://kilo.ai/api/models/stats) as **coding-task evidence**. Terminal-Bench 2.0 completion does not measure QA bug detection, brand design, translation quality, or documentation correctness. Code Mode Rank is seven-day usage, not an independent quality score. Missing metric values are unknown, not zero.
3. Compose small packs with distinct roles: an initial model, a cheaper iteration route where helpful, and a reviewer or multimodal specialist. Extras are opt-in and must not be added silently. Gateway prices are dynamic; none are hard-coded into the pack definition.
4. Validate each pack with representative tasks and the actual agent/protocol before promoting it beyond “candidate.” No paid inference was performed while curating these definitions.

## QA · Bug Hunter

- **Default: DeepSeek V4.1 Flash** for iterative cases and regressions. Its result on Kilo's terminal coding benchmark is a useful proxy for coding tasks, not proof of test quality.
- **GPT-5.6 Sol** for complex reproductions and repairs. **Claude Opus 5.5** for an independent, premium review; [Anthropic's release](https://www.anthropic.com/claude-opus-5-5) includes provider and partner observations about code review, which should be replicated on real bugs before making quality claims.
- **Optional: Gemini 3.8 Flash** for visual QA, subject to screenshot and client compatibility checks.
- **Evaluation gate:** reproduce known bugs, tests that fail before and pass after a fix, false-positive rate, and total time/cost per verified issue.

## Full Stack Dev

- **Default: GPT-6 Sol** for daily coding; it is a candidate based on Kilo's current catalog and use, not a claimed benchmark winner for this exact ID.
- **DeepSeek V4.1 Flash** for economical iteration; **Claude Sonnet 5** for a second-provider review.
- **Optional: GPT-6 Astra** only by user choice for expensive escalations; the coding benchmark and its cost per attempt are shown on [Kilo's leaderboard](https://kilo.ai/leaderboard).
- **Evaluation gate:** repository changes that compile and pass tests, unintended changes in diffs, tool compatibility, and total cost/time for a completed task.

## Design Studio

- **Default: Claude Opus 5.5** for direction and polish. [Anthropic](https://www.anthropic.com/claude-opus-5-5) reports improvements on a visual/game-building exercise, not a general UX or brand benchmark.
- **GLM-5.3 Flash** for less expensive screenshot-driven implementation; [Z.ai's model guide](https://docs.z.ai/guides/vlm/glm-5.3-flash) explicitly describes visual UI coding. **Gemini 3.8 Flash** for another multimodal reading of reference material; [Google's model guide](https://ai.google.dev/gemini-api/docs/models/gemini-3.8-flash) lists image, video, audio, and PDF input.
- **Optional: Claude Sonnet 5** for a different cost tier.
- **Evaluation gate:** real reference-screen fidelity, responsive layouts, accessibility, implementation correctness, and human design critique.

## Docs · Multilingual

- **Default: GLM-5.3** for organizing long text sources. [Z.ai documents text-only input](https://docs.z.ai/guides/llm/glm-5.3), so images/PDFs need another route or extraction first.
- **Qwen3.8 Max 0902** as a candidate for ES/EN/ZH technical editing, not a proven winner on this exact gateway variant. The [Qwen3.8 project](https://github.com/QwenLM/Qwen3.8) describes professional and research tasks; it does not constitute a documentation-quality evaluation of this ID. **DeepSeek V4.1 Flash** is the lower-cost drafting candidate.
- **Optional: Kimi K3** for long-source synthesis; [Kimi](https://www.kimi.com/en) positions K3 for knowledge work, but its benefit and cost in this workflow still need measurement.
- **Evaluation gate:** every claim grounded in sources, correct API examples, ES/EN/ZH terminology review by humans, missing/invented references, and cost per publishable page.

## Marketing · Growth

This pack is **experimental**: Claude Sonnet 5 for the first draft, GPT-6 Luna for inexpensive variants, Gemini 3.8 Flash for multimodal reference material, and Qwen3.8 Max 0902 as an optional localization candidate. It must pass blind human brand-voice, accuracy, and cultural-localization reviews before being presented as a quality recommendation.

## Personal packs and updates

Built-ins are read-only, embedded templates and change with an app release. Under **Models → My packs**, a user can make an independent copy, create a new pack, add/remove catalog models, reorder them, change the default, name models' roles, and assign the result per supported agent. The copy is stored in private `model-packs.json`, separately from the existing shared `models.json`; built-in updates cannot overwrite it. Editing or switching a personal pack does **not** require reinstalling Kilo Proxy. Reopen an agent to load its changed profile; terminal launchers read the same saved assignment at their next invocation. Xcode and Open Design remain on the shared library in this first version.
