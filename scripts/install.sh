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

if [ -e "${INSTALL_DIR}" ] && [ "${MAGPIE_FORCE:-0}" != "1" ]; then
  echo "Refusing to install into existing path: ${INSTALL_DIR}" >&2
  echo "Delete it or re-run with MAGPIE_FORCE=1." >&2
  exit 1
fi

rm -rf "${INSTALL_DIR}"
mkdir -p "${INSTALL_DIR}"

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

generate_random_hex() {
  local bytes="$1"

  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex "${bytes}"
    return
  fi

  od -An -N "${bytes}" -tx1 /dev/urandom | tr -d ' \n'
}

echo "Downloading docker-compose.yml..."
download "${COMPOSE_URL}" "${INSTALL_DIR}/docker-compose.yml"

echo "Downloading .env.example..."
if ! download "${ENV_EXAMPLE_URL}" "${INSTALL_DIR}/.env.example"; then
  : # best-effort; we'll still proceed
fi

cd "${INSTALL_DIR}"

if [ -n "${PROXY_ENCRYPTION_KEY:-}" ]; then
  key="${PROXY_ENCRYPTION_KEY}"
else
  key=""
  key2=""
  fd=0
  opened_fd=0
  if [ -t 0 ]; then
    fd=0
  elif exec 3</dev/tty; then
    fd=3
    opened_fd=3
  else
    echo "PROXY_ENCRYPTION_KEY must be set when running non-interactively." >&2
    exit 1
  fi

  printf "Enter PROXY_ENCRYPTION_KEY (will be saved to .env):" >&2
  if ! IFS= read -r -s -u "${fd}" key; then
    echo "Failed to read PROXY_ENCRYPTION_KEY from terminal." >&2
    exit 1
  fi
  printf "\n" >&2
  if [ -z "${key}" ]; then
    echo "PROXY_ENCRYPTION_KEY cannot be empty." >&2
    exit 1
  fi
  printf "Confirm PROXY_ENCRYPTION_KEY:" >&2
  if ! IFS= read -r -s -u "${fd}" key2; then
    echo "Failed to read PROXY_ENCRYPTION_KEY confirmation from terminal." >&2
    exit 1
  fi
  printf "\n" >&2
  if [ "${key}" != "${key2}" ]; then
    echo "Keys did not match." >&2
    exit 1
  fi
  if [ "${opened_fd}" -eq 3 ]; then
    exec 3<&-
  fi
fi

if [[ "${key}" == *$'\n'* || "${key}" == *$'\r'* ]]; then
  echo "PROXY_ENCRYPTION_KEY must be a single line." >&2
  exit 1
fi

if [ -n "${JWT_SECRET:-}" ]; then
  jwt_secret="${JWT_SECRET}"
else
  jwt_secret=""
fi

if [[ "${jwt_secret}" == *$'\n'* || "${jwt_secret}" == *$'\r'* ]]; then
  echo "JWT_SECRET must be a single line." >&2
  exit 1
fi

if [ -n "${DB_USERNAME:-}" ]; then
  db_username="${DB_USERNAME}"
else
  db_username="magpie_$(generate_random_hex 6)"
fi

if [ -n "${DB_PASSWORD:-}" ]; then
  db_password="${DB_PASSWORD}"
else
  db_password="$(generate_random_hex 20)"
fi

db_name="${DB_NAME:-magpie}"

if [[ -z "${db_username}" ]]; then
  echo "DB_USERNAME cannot be empty." >&2
  exit 1
fi
if [[ -z "${db_password}" ]]; then
  echo "DB_PASSWORD cannot be empty." >&2
  exit 1
fi
if [[ -z "${db_name}" ]]; then
  echo "DB_NAME cannot be empty." >&2
  exit 1
fi
if [[ "${db_username}" == *$'\n'* || "${db_username}" == *$'\r'* ]]; then
  echo "DB_USERNAME must be a single line." >&2
  exit 1
fi
if [[ "${db_password}" == *$'\n'* || "${db_password}" == *$'\r'* ]]; then
  echo "DB_PASSWORD must be a single line." >&2
  exit 1
fi
if [[ "${db_name}" == *$'\n'* || "${db_name}" == *$'\r'* ]]; then
  echo "DB_NAME must be a single line." >&2
  exit 1
fi

backend_image="${MAGPIE_BACKEND_IMAGE:-}"
frontend_image="${MAGPIE_FRONTEND_IMAGE:-}"
backend_tag="${MAGPIE_BACKEND_TAG:-${MAGPIE_IMAGE_TAG:-}}"
frontend_tag="${MAGPIE_FRONTEND_TAG:-${MAGPIE_IMAGE_TAG:-}}"

for image_setting in "${backend_image}" "${frontend_image}" "${backend_tag}" "${frontend_tag}"; do
  if [[ "${image_setting}" == *$'\n'* || "${image_setting}" == *$'\r'* ]]; then
    echo "Magpie image names and tags must be single-line values." >&2
    exit 1
  fi
done

escaped_key="${key//\\/\\\\}"
escaped_key="${escaped_key//\"/\\\"}"
escaped_jwt_secret="${jwt_secret//\\/\\\\}"
escaped_jwt_secret="${escaped_jwt_secret//\"/\\\"}"
escaped_db_name="${db_name//\\/\\\\}"
escaped_db_name="${escaped_db_name//\"/\\\"}"
escaped_db_username="${db_username//\\/\\\\}"
escaped_db_username="${escaped_db_username//\"/\\\"}"
escaped_db_password="${db_password//\\/\\\\}"
escaped_db_password="${escaped_db_password//\"/\\\"}"
escaped_backend_image="${backend_image//\\/\\\\}"
escaped_backend_image="${escaped_backend_image//\"/\\\"}"
escaped_frontend_image="${frontend_image//\\/\\\\}"
escaped_frontend_image="${escaped_frontend_image//\"/\\\"}"
escaped_backend_tag="${backend_tag//\\/\\\\}"
escaped_backend_tag="${escaped_backend_tag//\"/\\\"}"
escaped_frontend_tag="${frontend_tag//\\/\\\\}"
escaped_frontend_tag="${escaped_frontend_tag//\"/\\\"}"

umask 077
{
  printf "PROXY_ENCRYPTION_KEY=\"%s\"\n" "${escaped_key}"
  if [ -n "${jwt_secret}" ]; then
    printf "JWT_SECRET=\"%s\"\n" "${escaped_jwt_secret}"
  fi
  printf "DB_NAME=\"%s\"\n" "${escaped_db_name}"
  printf "DB_USERNAME=\"%s\"\n" "${escaped_db_username}"
  printf "DB_PASSWORD=\"%s\"\n" "${escaped_db_password}"
  if [ -n "${backend_image}" ]; then
    printf "MAGPIE_BACKEND_IMAGE=\"%s\"\n" "${escaped_backend_image}"
  fi
  if [ -n "${backend_tag}" ]; then
    printf "MAGPIE_BACKEND_TAG=\"%s\"\n" "${escaped_backend_tag}"
  fi
  if [ -n "${frontend_image}" ]; then
    printf "MAGPIE_FRONTEND_IMAGE=\"%s\"\n" "${escaped_frontend_image}"
  fi
  if [ -n "${frontend_tag}" ]; then
    printf "MAGPIE_FRONTEND_TAG=\"%s\"\n" "${escaped_frontend_tag}"
  fi
} > .env

echo "Pulling images..."
"${compose_cmd[@]}" -f docker-compose.yml pull

echo "Starting Magpie..."
"${compose_cmd[@]}" -f docker-compose.yml up -d

compose_display="${compose_cmd[*]}"
cat <<EOF

Magpie is up.
- UI:  http://localhost:5050
- API: http://localhost:5656/api

To stop:
  cd "${INSTALL_DIR}" && ${compose_display} down
EOF
