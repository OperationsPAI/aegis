# =============================================================================
# RCABench Frontend Justfile
# =============================================================================
# Use 'just --list' to view all available commands

set dotenv-load := true
set dotenv-filename := ".env.local"

# Configuration
npm_token := env_var_or_default("NPM_TOKEN", "")
registry := env_var_or_default("REGISTRY", "docker.io/opspai")
image := "rcabench-frontend"

root := justfile_directory()

# Colors
blue   := '\033[1;34m'
green  := '\033[1;32m'
yellow := '\033[1;33m'
red    := '\033[1;31m'
cyan   := '\033[1;36m'
gray   := '\033[90m'
reset  := '\033[0m'

# Use short commit hash as version
version := `git rev-parse --short HEAD`

# =============================================================================
# Default Recipe
# =============================================================================

# 📖 Display all available commands
default:
    @just --list

# =============================================================================
# Local Development Recipes
# =============================================================================

local-install:
    LOCAL_API=true pnpm install

local-debug:
    LOCAL_API=true pnpm run dev --host

# =============================================================================
# Build and Push Recipes
# =============================================================================

build tag=version:
    @if [ -z "{{npm_token}}" ]; then \
        echo "Error: NPM_TOKEN is not defined in .env.local"; \
        exit 1; \
    fi
    export NPM_TOKEN="{{npm_token}}" && \
    docker build \
        --network=host \
        --secret id=NPM_TOKEN,env=NPM_TOKEN \
        -t {{registry}}/{{image}}:{{tag}} .

push tag=version: (build tag)
    docker tag {{registry}}/{{image}}:{{tag}} {{registry}}/{{image}}:latest
    docker push {{registry}}/{{image}}:{{tag}}
    docker push {{registry}}/{{image}}:latest

# =============================================================================
# Utilities
# =============================================================================

update-version version:
    #!/usr/bin/env bash
    if command -v jq >/dev/null 2>&1; then \
        jq --arg v "{{version}}" '.version = $v' package.json > package.json.tmp && mv package.json.tmp package.json; \
    else \
        sed -i 's/"version": "[^"]*"/"version": "{{version}}"/' package.json; \
    fi
    if command -v yq >/dev/null 2>&1; then \
        yq -i '.appVersion = "{{version}}"' helm/Chart.yaml; \
    else \
        sed -i 's/^appVersion:.*/appVersion: {{version}}/' helm/Chart.yaml; \
    fi
    printf "{{green}}✅ Updated version to {{version}} in package.json and helm/Chart.yaml{{reset}}\n"

release version:
    #!/usr/bin/env bash
    set -euo pipefail
    printf "{{blue}}🚀 Releasing version {{version}}...{{reset}}\n"
    just update-version {{version}}
    pnpm run changelog
    git add {{root}}/CHANGELOG.md {{root}}/helm/Chart.yaml {{root}}/package.json
    git commit -m "chore(release): version {{version}}" --no-verify
    git push -u origin main
    git tag -a "v{{version}}" -m "Release version {{version}}"
    git push origin "v{{version}}"
    printf "{{green}}✅ Version {{version}} released successfully{{reset}}\n"