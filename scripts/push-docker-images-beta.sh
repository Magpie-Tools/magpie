#!/usr/bin/env bash
set -euo pipefail

IMAGE_FRONTEND="kuuchen/magpie-frontend-beta"
IMAGE_BACKEND="kuuchen/magpie-backend-beta"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required but not found in PATH." >&2
  exit 1
fi

if ! docker buildx version >/dev/null 2>&1; then
  echo "Docker Buildx is required but is not available." >&2
  exit 1
fi

tag="${1:-}"
if [[ -z "${tag}" ]]; then
  if git -C "${REPO_ROOT}" rev-parse --short HEAD >/dev/null 2>&1; then
    tag="$(git -C "${REPO_ROOT}" rev-parse --short HEAD)"
  else
    tag="$(date -u +%Y%m%d%H%M%S)"
  fi
fi

build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
push_latest="${PUSH_LATEST:-1}"
platforms="${DOCKER_PLATFORMS:-linux/amd64,linux/arm64}"
builder="${BUILDX_BUILDER:-magpie-multiarch}"

echo "Using tag: ${tag}"
echo "Using platforms: ${platforms}"
echo "Using Buildx builder: ${builder}"

if ! docker buildx inspect "${builder}" >/dev/null 2>&1; then
  echo "Creating Buildx builder ${builder}"
  docker buildx create --name "${builder}" --driver docker-container --use >/dev/null
else
  docker buildx use "${builder}" >/dev/null
fi

docker buildx inspect "${builder}" --bootstrap >/dev/null

backend_tags=(-t "${IMAGE_BACKEND}:${tag}")
frontend_tags=(-t "${IMAGE_FRONTEND}:${tag}")

if [[ "${push_latest}" == "1" ]]; then
  backend_tags+=(-t "${IMAGE_BACKEND}:latest")
  frontend_tags+=(-t "${IMAGE_FRONTEND}:latest")
fi

echo "Building and pushing backend image ${IMAGE_BACKEND}:${tag}"
docker buildx build -f "${REPO_ROOT}/Dockerfile" \
  --builder "${builder}" \
  --platform "${platforms}" \
  --build-arg BUILD_VERSION="${tag}" \
  --build-arg BUILD_TIME="${build_time}" \
  "${backend_tags[@]}" \
  --push \
  "${REPO_ROOT}"

echo "Building and pushing frontend image ${IMAGE_FRONTEND}:${tag}"
docker buildx build -f "${REPO_ROOT}/frontend/Dockerfile" \
  --builder "${builder}" \
  --platform "${platforms}" \
  --build-arg BUILD_COMMIT="${tag}" \
  "${frontend_tags[@]}" \
  --push \
  "${REPO_ROOT}/frontend"

if [[ "${push_latest}" == "1" ]]; then
  echo "Done. Pushed beta multi-arch images with tag ${tag} and latest."
else
  echo "Done. Pushed beta multi-arch images with tag ${tag}."
fi
