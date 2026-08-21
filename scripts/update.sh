#!/usr/bin/env bash
set -euo pipefail

REPO_OWNER="${MAGPIE_REPO_OWNER:-Magpie-Tools}"
REPO_NAME="${MAGPIE_REPO_NAME:-magpie}"
REPO_REF="${MAGPIE_REPO_REF:-master}"
if [[ "${REPO_REF}" == refs/* ]]; then
  REPO_REF_PATH="${REPO_REF}"
else
  REPO_REF_PATH="refs/heads/${REPO_REF}"
fi

INSTALL_DIR="${MAGPIE_INSTALL_DIR:-magpie}"
COMPOSE_URL="${MAGPIE_COMPOSE_URL:-https://raw.githubusercontent.com/${REPO_OWNER}/${REPO_NAME}/${REPO_REF_PATH}/docker-compose.yml}"
ENV_EXAMPLE_URL="${MAGPIE_ENV_EXAMPLE_URL:-https://raw.githubusercontent.com/${REPO_OWNER}/${REPO_NAME}/${REPO_REF_PATH}/.env.example}"

if [[ -z "${INSTALL_DIR}" || "${INSTALL_DIR}" == "/" ]]; then
  echo "MAGPIE_INSTALL_DIR must not be empty or '/'." >&2
  exit 1
fi

if [ ! -d "${INSTALL_DIR}" ]; then
  echo "Install directory not found: ${INSTALL_DIR}" >&2
  echo "Run the installer first or set MAGPIE_INSTALL_DIR." >&2
  exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required but was not found in PATH." >&2
  exit 1
fi

docker_cmd=(docker)
docker_needs_sudo=0

docker_err=""
if ! "${docker_cmd[@]}" info >/dev/null 2>&1; then
  docker_err="$("${docker_cmd[@]}" info 2>&1 || true)"
  if printf '%s' "${docker_err}" | grep -qi "permission denied" && [ "$(id -u)" -ne 0 ] && command -v sudo >/dev/null 2>&1; then
    echo "Docker socket requires elevated permissions; trying sudo..." >&2
    if sudo -n docker info >/dev/null 2>&1; then
      docker_cmd=(sudo docker)
      docker_needs_sudo=1
      docker_err=""
    else
      echo "Sudo is required for Docker; you may be prompted for your password." >&2
      if sudo docker info >/dev/null; then
        docker_cmd=(sudo docker)
        docker_needs_sudo=1
        docker_err=""
      else
        docker_err="$(sudo docker info 2>&1 || true)"
      fi
    fi
  fi
fi

if ! "${docker_cmd[@]}" info >/dev/null 2>&1; then
  err="${docker_err:-$("${docker_cmd[@]}" info 2>&1 || true)}"
  echo "Docker daemon not reachable from this shell." >&2
  if [ -n "${err}" ]; then
    echo >&2
    echo "Docker output:" >&2
    echo "${err}" >&2
  fi
  echo >&2
  if printf '%s' "${err}" | grep -qi "permission denied"; then
    echo "Tip (Linux): your user may not have access to the Docker socket." >&2
    echo "  - Try: sudo usermod -aG docker \"$USER\"  (then log out/in, or run: newgrp docker)" >&2
    echo "  - If you rerun with sudo, pipe bash through sudo (common gotcha):" >&2
    echo "      curl ... | sudo bash" >&2
    echo "    (NOT: sudo curl ... | bash  — that still runs bash as your user)" >&2
  else
    echo "Tip:" >&2
    echo "  - Ensure Docker Desktop/Engine is running" >&2
    echo "  - Check: docker context show && docker context ls" >&2
  fi
  exit 1
fi

if "${docker_cmd[@]}" compose version >/dev/null 2>&1; then
  compose_cmd=("${docker_cmd[@]}" compose)
elif command -v docker-compose >/dev/null 2>&1; then
  if [ "${docker_needs_sudo}" = "1" ]; then
    compose_cmd=(sudo docker-compose)
  else
    compose_cmd=(docker-compose)
  fi
else
  echo "Docker Compose is required but was not found. Install Docker Desktop or docker-compose." >&2
  exit 1
fi

download() {
  local url="$1"
  local dest="$2"

  if command -v curl >/dev/null 2>&1; then
    if ! curl -fsSL "${url}" -o "${dest}"; then
      echo "Download failed: ${url}" >&2
      return 1
    fi
  elif command -v wget >/dev/null 2>&1; then
    if ! wget -qO "${dest}" "${url}"; then
      echo "Download failed: ${url}" >&2
      return 1
    fi
  else
    echo "Need curl or wget to download files." >&2
    exit 1
  fi
}

cd "${INSTALL_DIR}"

required_secrets=("PROXY_ENCRYPTION_KEY")

if [ -f .env ]; then
  for secret_name in "${required_secrets[@]}"; do
    if command -v rg >/dev/null 2>&1; then
      has_secret="$(rg -q "^${secret_name}=" .env && echo yes || echo no)"
    else
      has_secret="$(grep -q "^${secret_name}=" .env && echo yes || echo no)"
    fi

    env_fallback="${!secret_name-}"
    if [ "${has_secret}" != "yes" ] && [ -z "${env_fallback}" ]; then
      echo "Missing ${secret_name} in ${INSTALL_DIR}/.env" >&2
      echo "Add ${secret_name} to .env or export ${secret_name} and rerun." >&2
      exit 1
    fi
  done
else
  missing=()
  for secret_name in "${required_secrets[@]}"; do
    if [ -z "${!secret_name-}" ]; then
      missing+=("${secret_name}")
    fi
  done

  if [ "${#missing[@]}" -gt 0 ]; then
    echo "Missing ${INSTALL_DIR}/.env (required secrets: ${missing[*]})." >&2
    echo "Restore it or export ${missing[*]} and rerun." >&2
    exit 1
  fi
fi

tmp_compose="docker-compose.yml.new.$$"

echo "Downloading latest docker-compose.yml..."
download "${COMPOSE_URL}" "${tmp_compose}"

if [ -f docker-compose.yml ]; then
  cp -f docker-compose.yml "docker-compose.yml.bak"
fi
mv -f "${tmp_compose}" docker-compose.yml

echo "Refreshing .env.example (optional)..."
download "${ENV_EXAMPLE_URL}" ".env.example" || true

echo "Pulling images..."
"${compose_cmd[@]}" -f docker-compose.yml pull

echo "Stopping backend for database migration..."
"${compose_cmd[@]}" -f docker-compose.yml stop backend

echo "Running database migration..."
"${compose_cmd[@]}" -f docker-compose.yml run --rm backend --migrate-only

echo "Applying update..."
"${compose_cmd[@]}" -f docker-compose.yml up -d

echo "Done."
