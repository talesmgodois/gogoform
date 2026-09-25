"""Creates the next task file: tasks/<NNNNNN>_<name>.md, numbered after the latest task."""
import re
import sys
from pathlib import Path

BASE_DIR = Path(__file__).resolve().parent
TASKS_DIR = BASE_DIR / "tasks"
# Every place a task file can live, so a number is never reused.
TASK_DIRS = (TASKS_DIR, BASE_DIR / "finished", BASE_DIR / "errors")
TASK_NUMBER_RE = re.compile(r"^(\d+)_")


def next_task_number():
    """Returns the highest task number found in TASK_DIRS plus one."""
    numbers = [
        int(match.group(1))
        for task_dir in TASK_DIRS if task_dir.is_dir()
        for path in task_dir.iterdir()
        if (match := TASK_NUMBER_RE.match(path.name))
    ]
    return max(numbers, default=0) + 1


def main():
    name = " ".join(sys.argv[1:]).strip()
    if not name:
        sys.exit("usage: new_task.py <name>")

    number = f"{next_task_number():06d}"
    slug = re.sub(r"\s+", "_", name)
    task_path = TASKS_DIR / f"{number}_{slug}.md"

    TASKS_DIR.mkdir(parents=True, exist_ok=True)
    with task_path.open("x", encoding="utf-8") as f:  # "x": never overwrite an existing task
        f.write(f"# {number} {name}\n\n")
    print(task_path.relative_to(BASE_DIR.parent))


if __name__ == "__main__":
    main()
