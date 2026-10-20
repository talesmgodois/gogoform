"""Prints the model ids usable with MODEL_PLAN/MODEL_IMPLEMENT/MODEL_REVIEW/MODEL_COMMITS.

Which list is shown depends on AGENT_CLI (default "claude"), the same variable runner.py
uses to pick the agent. Claude Code has no models subcommand, so its ids are scraped from
the binary's model catalog; opencode exposes them via `opencode models`.

Usage:
    python3 __ai_work/list_models.py           # models for $AGENT_CLI (default: claude)
    python3 __ai_work/list_models.py --all     # both CLIs
    AGENT_CLI=opencode python3 __ai_work/list_models.py
"""
import re
import shutil
import subprocess
import sys
import os

# Short names claude accepts in addition to full ids.
CLAUDE_ALIASES = ("opus", "sonnet", "haiku", "inherit")

# Full ids look like claude-sonnet-4-5 / claude-opus-4-5-20251101; skip catalog
# fragments and internal "-v1" variants that also appear as strings in the binary.
CLAUDE_MODEL_RE = re.compile(r"^claude-(?:opus|sonnet|haiku|fable)-\d[\w.-]*$")


def claude_binary():
    path = shutil.which("claude")
    if not path:
        sys.exit("claude CLI not found on PATH")
    return os.path.realpath(path)


def claude_models():
    """Model ids from the claude binary's embedded model catalog, plus the aliases."""
    result = subprocess.run(["strings", claude_binary()], capture_output=True, text=True)
    if result.returncode != 0:
        sys.exit(f"strings failed on the claude binary: {result.stderr.strip()}")
    ids = {
        match.group(0)
        for line in result.stdout.splitlines()
        for match in [re.search(r"claude-(?:opus|sonnet|haiku|fable)[\w.-]*", line)]
        if match and CLAUDE_MODEL_RE.match(match.group(0)) and not match.group(0).endswith("-v1")
    }
    return list(CLAUDE_ALIASES), sorted(ids)


def opencode_models():
    """Model ids from `opencode models` (one provider/model per line)."""
    if not shutil.which("opencode"):
        sys.exit("opencode CLI not found on PATH")
    result = subprocess.run(["opencode", "models"], capture_output=True, text=True)
    if result.returncode != 0:
        sys.exit(f"opencode models failed: {result.stderr.strip()}")
    ids = [line.strip() for line in result.stdout.splitlines() if line.strip()]
    return [], sorted(ids)


def print_cli(name, aliases, ids):
    print(f"== {name} ==")
    if aliases:
        print("aliases (short names accepted by --model):")
        for alias in aliases:
            print(f"  {alias}")
    print(f"model ids ({len(ids)}):")
    for model in ids:
        print(f"  {model}")
    print()


def main():
    cli = os.environ.get("AGENT_CLI", "claude")
    show_all = "--all" in sys.argv[1:]
    if show_all or cli == "claude":
        print_cli("claude", *claude_models())
    if show_all or cli == "opencode":
        print_cli("opencode", *opencode_models())
    if not show_all and cli not in ("claude", "opencode"):
        sys.exit(f"unknown AGENT_CLI {cli!r} (expected claude or opencode)")


if __name__ == "__main__":
    main()
