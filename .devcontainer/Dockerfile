# syntax=docker/dockerfile:1

# Dev image for gogoform: Go + Python (via devcontainer features in
# devcontainer.json), plus bun.sh, OpenCode and Claude Code installed here.
FROM mcr.microsoft.com/devcontainers/base:ubuntu-24.04

# Shared home for the agent CLIs. Installed as root during the image build and
# exposed through /usr/local/bin, so the runtime user (vscode) can run them and
# self-update them (the directory is chown'd below).
ARG AGENT_HOME=/opt/agent-tools

USER root

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential \
        ca-certificates \
        curl \
        git-lfs \
        jq \
        unzip \
        zstd \
    && rm -rf /var/lib/apt/lists/*

# Mount points for the config volumes declared in devcontainer.json. Docker
# seeds an empty named volume from the image, so these directories must exist
# here with the runtime user's ownership; otherwise a fresh volume is created
# root-owned and post-create.sh cannot write into it.
RUN mkdir -p /home/vscode/.claude /home/vscode/.config/opencode \
    && chown -R vscode:vscode /home/vscode/.claude /home/vscode/.config/opencode

# bun.sh, installed system-wide: the installer honours BUN_INSTALL and skips
# writing shell rc files when the target is already on PATH.
RUN curl -fsSL https://bun.sh/install | BUN_INSTALL=/usr/local bash \
    && bun --version

# OpenCode and Claude Code via their official installers. Both install into
# $HOME, so the install runs with HOME pointed at AGENT_HOME instead of /root;
# --no-modify-path keeps the installer from editing a shell rc we never read.
RUN mkdir -p "$AGENT_HOME" \
    && curl -fsSL https://opencode.ai/install \
        | HOME="$AGENT_HOME" bash -s -- --no-modify-path \
    && curl -fsSL https://claude.ai/install.sh | HOME="$AGENT_HOME" bash \
    && ln -sf "$AGENT_HOME/.opencode/bin/opencode" /usr/local/bin/opencode \
    && ln -sf "$AGENT_HOME/.local/bin/claude" /usr/local/bin/claude \
    && chmod -R a+rX "$AGENT_HOME" \
    && chown -R vscode:vscode "$AGENT_HOME" \
    && opencode --version \
    && claude --version \
    && bun --version
