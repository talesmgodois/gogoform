import os
import re
import shutil
import subprocess
from pathlib import Path

BASE_DIR = Path(__file__).resolve().parent
PROJECT_ROOT = BASE_DIR.parent

TASKS_DIR = BASE_DIR / "tasks"
FINISHED_DIR = BASE_DIR / "finished"
ERRORS_DIR = BASE_DIR / "errors"
WORKDIR = PROJECT_ROOT / "workdir"

FINISHED_DIR.mkdir(parents=True, exist_ok=True)
ERRORS_DIR.mkdir(parents=True, exist_ok=True)


def run_command(command, cwd=WORKDIR, check=True):
    """Utility to run shell commands safely."""
    result = subprocess.run(
        command,
        cwd=cwd,
        text=True,
        capture_output=True,
        shell=isinstance(command, str)
    )
    if check and result.returncode != 0:
        raise RuntimeError(f"Command failed: {command}\nError: {result.stderr}")
    return result


def extract_task_id(filename):
    """Extracts '000001' from '000001_scaffold_program.md'."""
    match = re.match(r"^(\d+)", filename)
    return match.group(1) if match else filename.split("_")[0]


def prompt_user_choice(question, options):
    """Prompts interactively for one of several (key, label) options. Returns the chosen key."""
    valid_keys = {key.lower() for key, _ in options}
    while True:
        print(f"\n{question}")
        for key, label in options:
            print(f"  [{key}] {label}")
        choice = input("Your choice: ").strip().lower()
        if choice in valid_keys:
            return choice
        print(f"⚠️  Invalid choice '{choice}'. Please pick one of: {', '.join(sorted(valid_keys))}")


def handle_dirty_worktree(cwd=PROJECT_ROOT):
    """Detects uncommitted changes and asks the user how to proceed before switching branches.

    Runs at the start of every branch preparation step: an interrupted previous
    run (implementation failures currently leave changes uncommitted) or manual
    edits could otherwise be silently carried into the wrong branch, or block
    the upcoming 'git checkout main'.
    """
    status = run_command("git status --porcelain", cwd=cwd, check=False).stdout.strip()
    if not status:
        print("🧹 Working tree is clean.")
        return

    print("\n⚠️  Uncommitted changes detected in the working tree:")
    print(status)

    choice = prompt_user_choice(
        "How would you like to proceed before switching branches?",
        [
            ("1", "Stash changes (git stash -u) and continue"),
            ("2", "Commit changes now via the /commits skill, then continue"),
            ("3", "Discard all changes (DESTRUCTIVE: git checkout -- . && git clean -fd)"),
            ("4", "Abort the run"),
        ],
    )

    if choice == "1":
        print("📦 Stashing changes...")
        run_command("git stash push -u -m 'runner.py auto-stash'", cwd=cwd)
        print("✅ Changes stashed. They can be restored later with 'git stash pop'.")
    elif choice == "2":
        print("📦 Committing changes now via /commits skill...")
        if run_grouped_commits():
            print("✅ Changes committed.")
        else:
            leftover = run_command("git status --porcelain", cwd=cwd, check=False).stdout.strip()
            if leftover:
                print("❌ Changes still remain uncommitted after /commits. Aborting run so nothing gets lost.")
                raise SystemExit(1)
    elif choice == "3":
        confirm = input("Type 'discard' to permanently discard these changes: ").strip().lower()
        if confirm != "discard":
            print("❌ Discard not confirmed. Aborting run.")
            raise SystemExit(1)
        print("🗑️  Discarding all local changes...")
        run_command("git checkout -- .", cwd=cwd, check=False)
        run_command("git clean -fd", cwd=cwd, check=False)
        print("✅ Working tree cleaned.")
    else:
        print("🛑 Aborting run at user's request.")
        raise SystemExit(0)


def prepare_git_branch(task_filename):
    """Syncs main repo and creates a dedicated feat/<task_id> branch."""
    task_id = extract_task_id(task_filename)
    branch_name = f"feat/{task_id}"

    print(f"\n🔄 Preparing Git branch '{branch_name}' for task '{task_filename}'...")

    # Guard against losing in-progress work: ask the user how to proceed
    # if the working tree has uncommitted changes before switching branches.
    handle_dirty_worktree()

    # A fresh repo has no commits, so "main" doesn't exist yet and can't be checked out.
    # Give it an empty root commit to branch from.
    if run_command("git rev-parse --verify HEAD", check=False).returncode != 0:
        print("🌱 Repository has no commits; creating an empty initial commit on 'main'...")
        run_command("git checkout -B main")
        run_command(["git", "commit", "--allow-empty", "-m", "chore: initial commit"])

    print("📍 Checking out 'main'...")
    run_command("git checkout main")

    print("⬇️  Pulling latest 'main' from remote (if any)...")
    pull_result = run_command("git pull origin main", check=False)
    if pull_result.returncode != 0:
        print(f"ℹ️  Skipped pull (no remote configured yet, or nothing to pull): {pull_result.stderr.strip()}")

    print(f"🌿 Creating/checking out feature branch '{branch_name}'...")
    run_command(f"git checkout -B {branch_name}")
    print(f"✅ Now on branch '{branch_name}'.")

    return branch_name


# Which CLI drives the agent: "claude" (Claude Code) or "opencode".
AGENT_CLI = os.environ.get("AGENT_CLI", "claude")

VERDICT_RE = re.compile(r"^VERDICT:\s*(PASS|FAIL)\b:?\s*(.*)$", re.MULTILINE)
COMMITS_RESULT_RE = re.compile(r"^RESULT:\s*(OK|PARTIAL|NOTHING_TO_COMMIT)\b:?\s*(.*)$", re.MULTILINE)


def run_agent(prompt=None, command=None, args="", cwd=WORKDIR, capture=False):
    """Runs the agent CLI headlessly with a free-form prompt or a skill/command.

    Returns (success, stdout). stdout is only captured when capture=True.
    """
    if AGENT_CLI == "opencode":
        cmd = ["opencode", "run"]
        if command:
            cmd += ["--command", command]
            if args:
                cmd.append(args)
        else:
            cmd.append(prompt)
    else:
        text = f"/{command} {args}".strip() if command else prompt
        cmd = ["claude", "-p", text, "--dangerously-skip-permissions"]

    result = subprocess.run(cmd, cwd=cwd, text=True, capture_output=capture)
    if capture:
        print(result.stdout)
        if result.stderr:
            print(result.stderr)
    return result.returncode == 0, result.stdout or ""


def execute_claude(prompt_text):
    """Runs the agent with the task prompt."""
    success, _ = run_agent(prompt=prompt_text)
    return success


def run_code_review(task_filename):
    """Runs the /code-review skill and gates on its VERDICT line.

    Returns (passed, reason). The full report is saved next to the task in errors/ on failure.
    """
    print("\n🔍 Running automated Code Review agent...")
    ok, output = run_agent(command="code-review", cwd=PROJECT_ROOT, capture=True)

    # Use the last VERDICT line, in case the report quotes the format earlier.
    matches = VERDICT_RE.findall(output)
    if not ok or not matches:
        verdict, reason = "FAIL", "review agent crashed or produced no VERDICT line"
    else:
        verdict, reason = matches[-1]

    print(f"🧾 Code review verdict: {verdict}" + (f" — {reason}" if reason else ""))

    if verdict != "PASS":
        report_path = ERRORS_DIR / f"{Path(task_filename).stem}.review.md"
        report_path.write_text(output, encoding="utf-8")
        print(f"📝 Review report saved to {report_path}")
        return False, reason
    return True, ""


def push_branch(branch_name):
    """Pushes the branch to the remote, setting upstream with -u."""
    print(f"\n⬆️  Pushing branch '{branch_name}' to remote...")
    result = run_command(f"git push -u origin {branch_name}", cwd=PROJECT_ROOT, check=False)
    if result.returncode != 0:
        print(f"⚠️  Failed to push branch '{branch_name}':\n{result.stderr.strip()}")
        return False
    print(f"✅ Pushed '{branch_name}' to remote (upstream set).")
    return True


def merge_to_main(branch_name):
    """Merges the feature branch into main locally."""
    print(f"\n🔀 Merging '{branch_name}' into main...")
    run_command("git checkout main", cwd=PROJECT_ROOT)
    result = run_command(f"git merge --no-ff {branch_name}", cwd=PROJECT_ROOT, check=False)
    if result.returncode != 0:
        print(f"⚠️  Failed to merge '{branch_name}' into main:\n{result.stderr.strip()}")
        print("↩️  Aborting the in-progress merge to keep 'main' clean...")
        run_command("git merge --abort", cwd=PROJECT_ROOT, check=False)
        return False
    print(f"✅ Merged '{branch_name}' into main.")
    return True


def run_grouped_commits():
    """Triggers the /commits skill to group and commit remaining changes."""
    print("\n📦 Grouping and committing changes via /commits skill...")
    ok, output = run_agent(command="commits", cwd=PROJECT_ROOT, capture=True)

    matches = COMMITS_RESULT_RE.findall(output)
    status = matches[-1][0] if matches else "UNKNOWN"
    print(f"🧾 Commits result: {status}")
    leftover = run_command("git status --porcelain", cwd=PROJECT_ROOT, check=False).stdout.strip()
    if leftover:
        print(f"⚠️  Uncommitted changes remain after /commits:\n{leftover}")
    return ok and status in ("OK", "NOTHING_TO_COMMIT")


def process_tasks():
    tasks = sorted(
        [f for f in TASKS_DIR.iterdir() if f.is_file() and f.suffix in [".md", ".txt"]],
        key=lambda p: p.name
    )

    if not tasks:
        print("✨ No pending tasks found.")
        return

    print(f"📋 Found {len(tasks)} task(s) to process: {', '.join(t.name for t in tasks)}")

    completed, failed = [], []

    for task_path in tasks:
        print("\n" + "=" * 60)
        print(f"📋 Task: {task_path.name}")
        print("=" * 60)

        try:
            # 1. Sync Git & Create Feature Branch
            branch_name = prepare_git_branch(task_path.name)

            prompt_content = task_path.read_text(encoding="utf-8").strip()
            if not prompt_content:
                print(f"⚠️  Empty task {task_path.name}. Skipping...")
                continue

            # 2. Execute Task Implementation
            print(f"\n🤖 Executing task implementation for '{task_path.name}'...")
            success = execute_claude(prompt_content)
            print(f"{'✅' if success else '❌'} Implementation {'succeeded' if success else 'failed'} for '{task_path.name}'.")

            # 3. Perform Code Review & Commits if implementation succeeded
            if success:
                if task_path.name.endswith(".ncr.md"):
                    print(f"⏭️  Skipping code review for {task_path.name} (.ncr.md)")
                    review_passed, reason = True, ""
                else:
                    review_passed, reason = run_code_review(task_path.name)

                if review_passed:
                    # Move the task file first so its relocation is part of the commit.
                    shutil.move(str(task_path), str(FINISHED_DIR / task_path.name))
                    print(f"📁 Moved task file to {FINISHED_DIR / task_path.name}")
                    if run_grouped_commits():
                        if push_branch(branch_name) and merge_to_main(branch_name):
                            print(f"✅ Completed: {task_path.name}")
                            completed.append(task_path.name)
                        else:
                            print(f"⚠️  Completed with push/merge issues: {task_path.name}")
                            failed.append(task_path.name)
                    else:
                        print(f"⚠️  Completed with commit issues: {task_path.name}")
                        failed.append(task_path.name)
                else:
                    print(f"❌ Code review failed for: {task_path.name} — {reason}")
                    shutil.move(str(task_path), str(ERRORS_DIR / task_path.name))
                    failed.append(task_path.name)
            else:
                print(f"❌ Implementation failed for: {task_path.name}")
                shutil.move(str(task_path), str(ERRORS_DIR / task_path.name))
                failed.append(task_path.name)

        except SystemExit:
            raise
        except Exception as exc:
            print(f"💥 Unexpected error while processing {task_path.name}: {exc}")
            if task_path.exists():
                shutil.move(str(task_path), str(ERRORS_DIR / task_path.name))
                print(f"📁 Moved task file to {ERRORS_DIR / task_path.name}")
            failed.append(task_path.name)

    print("\n" + "=" * 60)
    print(f"🏁 Run finished: {len(completed)} completed, {len(failed)} failed.")
    if completed:
        print(f"   ✅ {', '.join(completed)}")
    if failed:
        print(f"   ❌ {', '.join(failed)}")
    print("=" * 60)


if __name__ == "__main__":
    process_tasks()