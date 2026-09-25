WORKDIR := workdir
# Every documented target of workdir/Makefile, forwarded below so it runs from
# the repo root. Its recipes use paths relative to workdir/, so they're run with
# `make -C` instead of `include`. Command-line variables (e.g. NAME=x) are passed along.
WORKDIR_TARGETS := $(filter-out help,$(shell sed -n 's/^\([a-zA-Z0-9_-]*\):.*\#\# .*/\1/p' $(WORKDIR)/Makefile))

.DEFAULT_GOAL := help
.PHONY: help new-task run-tasks $(WORKDIR_TARGETS)

help: ## Show commands and descriptions
	@awk 'BEGIN {FS = ":.*?## "; printf "Usage: make <target>\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*?## / {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' Makefile
	@awk 'BEGIN {FS = ":.*?## "; printf "\nApp targets (run in $(WORKDIR)/):\n"} /^[a-zA-Z0-9_-]+:.*?## / && $$1 != "help" {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(WORKDIR)/Makefile

new-task: ## Create the next task file in __ai_work/tasks: make new-task NAME=my_task
	@test -n "$(NAME)" || { echo "usage: make new-task NAME=<task_name>"; exit 1; }
	@python3 __ai_work/new_task.py "$(NAME)"

run-tasks: ## Run (or resume) the pending tasks in __ai_work/tasks (optional: AGENT_CLI=opencode MAX_FIX_ATTEMPTS=3 RUNNER_RESUME=0 AGENT_CLI_STDOUT=1)
	$(if $(AGENT_CLI),AGENT_CLI=$(AGENT_CLI)) $(if $(MAX_FIX_ATTEMPTS),MAX_FIX_ATTEMPTS=$(MAX_FIX_ATTEMPTS)) $(if $(RUNNER_RESUME),RUNNER_RESUME=$(RUNNER_RESUME)) $(if $(AGENT_CLI_STDOUT),AGENT_CLI_STDOUT=$(AGENT_CLI_STDOUT)) python3 __ai_work/runner.py

$(WORKDIR_TARGETS):
	@$(MAKE) --no-print-directory -C $(WORKDIR) $@
