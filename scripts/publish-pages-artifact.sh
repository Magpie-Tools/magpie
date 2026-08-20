#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "Usage: $0 SOURCE_DIR {root|docs} COMMIT_MESSAGE" >&2
}

if [ "$#" -ne 3 ]; then
  usage
  exit 2
fi

source_input="$1"
target="$2"
commit_message="$3"

if [ ! -d "${source_input}" ]; then
  echo "Pages artifact directory not found: ${source_input}" >&2
  exit 1
fi

source_dir="$(cd "${source_input}" && pwd)"
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
distribution_repo="$(git -C "${script_dir}/.." rev-parse --show-toplevel)"
remote="${MAGPIE_PAGES_REMOTE:-origin}"
branch="${MAGPIE_PAGES_BRANCH:-gh-pages}"
push_changes="${MAGPIE_DEPLOY_PUSH:-1}"
dry_run="${MAGPIE_DEPLOY_DRY_RUN:-0}"
worktree="${MAGPIE_PAGES_WORKTREE:-$(dirname "${distribution_repo}")/.magpie-gh-pages-worktree}"

case "${target}" in
  root|docs) ;;
  *)
    usage
    exit 2
    ;;
esac

case "${push_changes}" in
  0|1) ;;
  *)
    echo "MAGPIE_DEPLOY_PUSH must be 0 or 1." >&2
    exit 2
    ;;
esac

case "${dry_run}" in
  0|1) ;;
  *)
    echo "MAGPIE_DEPLOY_DRY_RUN must be 0 or 1." >&2
    exit 2
    ;;
esac

if [ ! -f "${source_dir}/index.html" ]; then
  echo "Pages artifact is missing index.html: ${source_dir}" >&2
  exit 1
fi

if [ "${target}" = "root" ] && [ ! -f "${source_dir}/CNAME" ]; then
  echo "Website artifact is missing its CNAME file." >&2
  exit 1
fi

if [ "${dry_run}" = "1" ]; then
  echo "Pages artifact validated for target '${target}': ${source_dir}"
  exit 0
fi

is_registered_worktree() {
  git -C "${distribution_repo}" worktree list --porcelain |
    grep -Fqx "worktree ${worktree}"
}

cleanup() {
  if is_registered_worktree; then
    git -C "${distribution_repo}" worktree remove "${worktree}" --force >/dev/null 2>&1 || true
  fi
  git -C "${distribution_repo}" worktree prune >/dev/null 2>&1 || true
}

trap cleanup EXIT

git -C "${distribution_repo}" worktree prune

if [ -e "${worktree}" ]; then
  if is_registered_worktree; then
    git -C "${distribution_repo}" worktree remove "${worktree}" --force
  else
    echo "Refusing to replace a path that is not this repository's worktree: ${worktree}" >&2
    exit 1
  fi
fi

git -C "${distribution_repo}" fetch "${remote}" "${branch}"

remote_ref="refs/remotes/${remote}/${branch}"
if ! git -C "${distribution_repo}" show-ref --verify --quiet "${remote_ref}"; then
  echo "Remote Pages branch not found: ${remote}/${branch}" >&2
  exit 1
fi

if git -C "${distribution_repo}" show-ref --verify --quiet "refs/heads/${branch}"; then
  git -C "${distribution_repo}" worktree add "${worktree}" "${branch}"
else
  git -C "${distribution_repo}" worktree add -b "${branch}" "${worktree}" "${remote}/${branch}"
fi

git -C "${worktree}" merge --ff-only "${remote}/${branch}"

case "${target}" in
  root)
    find "${worktree}" -mindepth 1 -maxdepth 1 \
      ! -name '.git' ! -name 'docs' -exec rm -rf -- {} +
    cp -a "${source_dir}"/. "${worktree}"/
    ;;
  docs)
    target_dir="${worktree}/docs"
    mkdir -p "${target_dir}"
    find "${target_dir}" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
    cp -a "${source_dir}"/. "${target_dir}"/
    ;;
esac

touch "${worktree}/.nojekyll"

git -C "${worktree}" add -A
if git -C "${worktree}" diff --cached --quiet; then
  echo "No Pages changes to commit for target '${target}'."
else
  git -C "${worktree}" commit -m "${commit_message}"
fi

if [ "${push_changes}" = "1" ]; then
  git -C "${worktree}" push "${remote}" "${branch}"
  echo "Pages branch pushed to ${remote}/${branch}."
else
  echo "Pages commit kept locally on ${branch}; push skipped."
fi
