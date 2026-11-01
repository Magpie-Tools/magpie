#!/usr/bin/env bash
set -euo pipefail

IMAGE="${1:?Usage: validate-container-startup.sh BACKEND_IMAGE}"
TASK_PREFIX="magpie-startup-check-$$"
NETWORK="${TASK_PREFIX}-net"
DATA_VOLUME="${TASK_PREFIX}-data"
CACHE_VOLUME="${TASK_PREFIX}-cache"
cleanup() {
  docker rm -f "${TASK_PREFIX}-backend" "${TASK_PREFIX}-postgres" "${TASK_PREFIX}-redis" >/dev/null 2>&1 || true
  docker volume rm "${DATA_VOLUME}" "${CACHE_VOLUME}" >/dev/null 2>&1 || true
  docker network rm "${NETWORK}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker network create "${NETWORK}" >/dev/null
docker volume create "${DATA_VOLUME}" >/dev/null
docker volume create "${CACHE_VOLUME}" >/dev/null
docker run -d --name "${TASK_PREFIX}-postgres" --network "${NETWORK}" --network-alias postgres \
  -e POSTGRES_USER=startup_test -e POSTGRES_PASSWORD=startup_test_isolated_password -e POSTGRES_DB=startup_test postgres:17-alpine >/dev/null
docker run -d --name "${TASK_PREFIX}-redis" --network "${NETWORK}" --network-alias redis redis:7-alpine >/dev/null
for attempt in {1..60}; do
  if docker exec "${TASK_PREFIX}-postgres" pg_isready -h 127.0.0.1 -U startup_test -d startup_test >/dev/null 2>&1; then break; fi
  if [ "${attempt}" = "60" ]; then echo "PostgreSQL fixture failed to start" >&2; exit 1; fi
  sleep 1
done

backend_args=(--network "${NETWORK}" \
  -e DB_HOST=postgres -e DB_PORT=5432 -e DB_USERNAME=startup_test -e DB_PASSWORD=startup_test_isolated_password -e DB_NAME=startup_test -e DB_SSLMODE=disable \
  -e PROXY_ENCRYPTION_KEY=startup-check-isolated-encryption-key-0123456789 -e JWT_SECRET= \
  -e REDIS_URL=redis://redis:6379 -e MAGPIE_ENV=production -e OBSERVABILITY_TOKEN=startup-check-observability-token \
  -e ALLOW_INSECURE_REGISTRATION_DEFAULTS=true \
  -v "${DATA_VOLUME}:/app/data" -v "${CACHE_VOLUME}:/home/nonroot/.cache/rod")

docker run --rm "${backend_args[@]}" "${IMAGE}" --migrate-only
for volume_case in fresh repaired; do
  if [ "${volume_case}" = "repaired" ]; then
    # Model a volume left by an older root-running image, then apply Compose's
    # ownership repair. The settings and generated JWT key must survive.
    docker run --rm -v "${DATA_VOLUME}:/app/data" -v "${CACHE_VOLUME}:/browser-cache" alpine:3.23 sh -ec \
      'chown -R 0:0 /app/data /browser-cache; chown -R 65532:65532 /app/data /browser-cache; chmod 700 /app/data /browser-cache'
  fi
  docker run -d --name "${TASK_PREFIX}-backend" --network-alias backend "${backend_args[@]}" "${IMAGE}" \
    -production -rod=bin=/usr/lib/chromium/chromium >/dev/null
  ready=false
  for attempt in {1..90}; do
    if docker run --rm --network "${NETWORK}" alpine:3.23 wget -q -O /dev/null \
      --header='X-Observability-Token: startup-check-observability-token' http://backend:5656/readyz; then
      ready=true
      break
    fi
    if [ "$(docker inspect -f '{{.State.Running}}' "${TASK_PREFIX}-backend")" != "true" ]; then break; fi
    sleep 1
  done
  if [ "${ready}" != "true" ]; then
    docker logs "${TASK_PREFIX}-backend" >&2
    echo "FAIL: ${volume_case} data-volume startup" >&2
    exit 1
  fi
  registration_response="$(docker run --rm --network "${NETWORK}" alpine:3.23 wget -S -O /dev/null \
    --header='Content-Type: application/json' \
    --post-data='{"email":"startup-test@magpie.test","password":"StartupTestPass!123"}' \
    http://backend:5656/api/register 2>&1 || true)"
  if ! [[ "${registration_response}" =~ HTTP/1\.[01][[:space:]]403 ]]; then
    echo "FAIL: production allowed first-admin bootstrap or returned an unexpected status" >&2
    echo "${registration_response}" >&2
    exit 1
  fi
  current_key_fingerprint="$(docker run --rm -v "${DATA_VOLUME}:/app/data:ro" alpine:3.23 sha256sum /app/data/jwt_secret)"
  if [ "${volume_case}" = "fresh" ]; then
    first_key_fingerprint="${current_key_fingerprint}"
  elif [ "${current_key_fingerprint}" != "${first_key_fingerprint}" ]; then
    echo "FAIL: volume repair/restart changed the JWT key" >&2
    exit 1
  fi
  docker stop -t 90 "${TASK_PREFIX}-backend" >/dev/null
  if [ "$(docker inspect -f '{{.State.ExitCode}}' "${TASK_PREFIX}-backend")" != "0" ]; then
    docker logs "${TASK_PREFIX}-backend" >&2
    echo "FAIL: graceful container shutdown" >&2
    exit 1
  fi
  docker rm "${TASK_PREFIX}-backend" >/dev/null
  echo "PASS: ${volume_case} data-volume production startup and graceful shutdown"
done
