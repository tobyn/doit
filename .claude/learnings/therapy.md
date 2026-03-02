# Therapy

Periodic reorganization of project memory to keep it useful as the
project grows. The developer will say "run therapy" (or similar).

## Goals

- **Reduce always-loaded context.** Rules files (`.claude/rules/`) are
  loaded into every conversation. They should contain behavioral
  instructions and concise signposts, not reference material.
- **Keep learnings loadable on demand.** Files in `.claude/learnings/`
  are loaded via signpost pointers when the topic is relevant. Each
  file should be self-contained — readable without needing to also
  load other learnings files.
- **Prune, don't hoard.** Learnings are working memory, not an
  append-only log. Update or remove information that is outdated,
  redundant with source code, or no longer useful.

## Memory tiers

Three tiers, cheapest to most expensive:

1. **Path-scoped rules** — frontmatter restricts to a file glob. Zero
   cost when not in scope. Best for implementation details tied to
   specific code paths.
2. **Signpost → learnings** — small pointer in `signposts.md` (always
   loaded), full content in `learnings/` (loaded on demand). Best for
   conceptual reference material not tied to a specific path.
3. **Always-loaded rules** — full content in every conversation.
   Reserve for behavioral instructions that genuinely affect all tasks.

## Ownership

`.claude/learnings/` is owned by the AI assistant. The developer will
not manually modify files there. The assistant can freely create,
update, reorganize, split, merge, and delete learnings files, and
create subdirectories as needed for organization.

`.claude/rules/signposts.md` must be kept in sync with learnings files.
When a learnings file is moved, split, or deleted, update the signpost.

## Process

1. **Assess current state.** Read `CLAUDE.md`, `signposts.md`, and list
   all rules and learnings files with their line counts.

2. **Check CLAUDE.md.** This is the highest-cost file — loaded in every
   conversation, no exceptions. Every line must earn its place:
   - Is each section a behavioral instruction that affects all tasks?
   - Is anything stale, redundant, or better placed in a rules file
     or learnings file?
   - Is the file concise and scannable?

3. **Check always-loaded rules.** For each non-path-scoped rules file,
   evaluate whether its content needs to be in every conversation:
   - If it's reference material → move to `learnings/`, add signpost.
   - If it's a behavioral instruction that affects all tasks → keep.
   - If it's stale or redundant → delete.

4. **Check learnings file health.** For each learnings file:
   - **Too large?** Split along task-oriented lines (what the developer
     is likely to ask you to do, not objective taxonomy).
   - **Outdated?** Update or remove sections that no longer reflect the
     codebase. Check against source code if uncertain.
   - **Redundant?** If information is better found by reading source
     code directly, consider removing it from learnings.
   - **Self-contained?** Each file should make sense when loaded alone.

5. **Check signpost accuracy.** Verify every signpost points to an
   existing file and every learnings file has a signpost entry (unless
   it's only referenced from within another learnings file).

6. **Report.** Summarize what changed and why. Note any files that are
   growing toward a future split.
