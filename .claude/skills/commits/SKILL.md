---
name: commits
description: Group all uncommitted changes (modified + untracked) into atomic Conventional Commits and commit them. Use when asked to commit, split changes into commits, or run /commits.
argument-hint: "[optional scope hint or extra instructions]"
allowed-tools: Bash(git status:*), Bash(git diff:*), Bash(git log:*), Bash(git add:*), Bash(git commit:*), Bash(git reset:*), Bash(git ls-files:*), Bash(git show:*), Bash(git apply:*), Read, Write
---

# /commits — atomic, grouped Conventional Commits

You are committing the current working tree as a series of small, logical commits.
Work non-interactively: do not ask questions, decide and commit.

Extra instructions from the caller (may be empty): $ARGUMENTS

## 1. Inspect

Run these and read the output carefully:

```bash
git status --porcelain=v1 -uall
git diff                    # unstaged changes
git diff --cached           # already-staged changes
git log --oneline -15       # match existing message style/scopes (may fail on a fresh repo; ignore)
```

For untracked files, read their contents (`Read` tool) — `git diff` does not show them.

If there is nothing to commit, print `NOTHING_TO_COMMIT` and stop.

If something is already staged, run `git reset -q` first so you group every change yourself.
On a repo with no commits yet, `git reset` fails. If that happens, skip it and include the staged files in your grouping as usual.

## 2. Group

Build a plan of commit groups. Rules:

- **One intent per commit.** A feature, a bug fix, a refactor, a docs change, a dependency bump — each is its own commit.
- **Tests travel with the code they test.** `foo.py` + `test_foo.py` for the same feature → one `feat`/`fix` commit. Tests added for *existing* untouched code → a separate `test` commit.
- **Refactors are separate from behavior changes.** If a file mixes both, split by hunk (see below). If hunks are too entangled to split safely, keep them together and use the type of the dominant behavior change.
- **Config/build/tooling** (`package.json`, lockfiles, `pyproject.toml`, CI, Dockerfiles) → `build:` or `ci:` or `chore:`, unless the change is required by a feature in the same set, in which case it goes with that feature (e.g. a new dependency the feature imports).
- **Order commits so every commit builds on its own**: foundations (config, deps, shared models/utils) first, then features that use them, then docs.
- Prefer 1–7 commits. Don't create a commit per file for a single cohesive change.

### Never commit

- Secrets or credentials: `.env*` (except `.env.example`), `*.pem`, `*.key`, `id_rsa*`, `credentials*.json`, files containing obvious API keys/tokens.
- Build output and caches: `node_modules/`, `dist/`, `build/`, `__pycache__/`, `*.pyc`, `.venv/`, `.DS_Store`, coverage reports.

Leave these unstaged and list them under "Skipped" in the final report. Do not edit `.gitignore` unless the caller asked for it.

## 3. Write messages (Conventional Commits)

```
<type>(<optional scope>): <imperative summary, lowercase, no period, ≤ 72 chars>

<optional body: WHY the change was made and anything non-obvious, wrapped at 72 cols>

<optional footer: BREAKING CHANGE: ..., Refs: #123>
```

Types: `feat`, `fix`, `refactor`, `perf`, `test`, `docs`, `style`, `build`, `ci`, `chore`, `revert`.
Scope = the module/package/area touched (e.g. `api`, `cli`, `core`, `db`), matching scopes already used in `git log` when possible.
Add `!` after type/scope and a `BREAKING CHANGE:` footer when a public API/contract changes incompatibly.

Good: `feat(api): add pagination to GET /quizzes`
Bad: `Updated files`, `feat: Added stuff.`, `fix: fix bug`

## 4. Execute

For each group, in order:

```bash
git add -- <path1> <path2> ...          # whole files
git commit -m "<subject>" -m "<body>"   # omit -m "<body>" if no body
```

Splitting a file by hunk: by default, stage the whole file with the group it mostly belongs to.
Split it only when the intents are in clearly separate hunks. To split, save `git diff -- <file>`
to a temp file, delete the hunks that belong to other groups, and stage the rest with
`git apply --cached <patch>`.

Rules:
- Always pass explicit paths to `git add`. Never `git add -A` / `git add .`.
- Never use `--no-verify`, `--amend`, `push`, `rebase`, `reset --hard`, `checkout --`, or `clean`.
- If a pre-commit hook fails: read the error, fix the issue if it's trivial and clearly within this change set (formatting, lint autofix), re-stage, and create a NEW commit attempt. Otherwise leave that group uncommitted and report it.
- After each commit, run `git status --porcelain` to confirm what's left.

## 5. Report

Finish with exactly this block (the orchestrator parses it):

```
COMMITS_CREATED: <n>
- <short sha> <subject>
- ...
SKIPPED: <paths left uncommitted, or "none">
RESULT: OK | PARTIAL: <reason> | NOTHING_TO_COMMIT
```
