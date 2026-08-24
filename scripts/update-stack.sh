#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

cd "${REPO_ROOT}"

if docker compose version >/dev/null 2>&1; then
  compose_cmd=(docker compose)
elif command -v docker-compose >/dev/null 2>&1; then
  compose_cmd=(docker-compose)
else
  echo "Docker Compose is required but was not found. Install Docker Desktop or docker-compose." >&2
  exit 1
fi

if [ ! -f .env ] && [ -z "${PROXY_ENCRYPTION_KEY:-}" ]; then
  echo "Missing .env (required for PROXY_ENCRYPTION_KEY)." >&2
  echo "Create it from .env.example (or export PROXY_ENCRYPTION_KEY) and rerun." >&2
  exit 1
fi

stash_applied=0
echo "Checking for local changes..."
if ! git diff --quiet --ignore-submodules -- || ! git diff --cached --quiet --ignore-submodules --; then
  echo "Local changes detected. Temporarily stashing..."
  if git stash push --include-untracked >/dev/null; then
    stash_applied=1
  else
    echo "Failed to stash local changes. Please resolve them manually and rerun." >&2
    exit 1
  fi
fi

echo "Pulling latest changes..."
if ! git pull --ff-only; then
  if [ "$stash_applied" -eq 1 ]; then
    echo "Restoring stashed changes after failed pull..."
    if ! git stash pop; then
      echo "Automatic restore of stashed changes failed. Run 'git stash pop' manually." >&2
    fi
  fi
  echo "Git pull failed. Resolve issues and rerun." >&2
  exit 1
fi

if [ "$stash_applied" -eq 1 ]; then
  echo "Restoring local changes..."
  if ! git stash pop; then
    echo "Automatic restore failed. Run 'git stash pop' manually." >&2
    exit 1
  fi
fi

echo "Pulling component and infrastructure images..."
"${compose_cmd[@]}" pull

echo "Migration safety: confirm you have coordinated PostgreSQL and Redis backups."
echo "Workspace migrations change resource ownership columns; rollback requires those backups."
echo "Stopping backend for database migration..."
"${compose_cmd[@]}" stop backend

echo "Running database migration..."
"${compose_cmd[@]}" run --rm backend --migrate-only

echo "Applying the updated stack..."
"${compose_cmd[@]}" up -d --remove-orphans

echo "Done. Frontend is available at http://localhost:5050 and backend API at http://localhost:5656/api"
