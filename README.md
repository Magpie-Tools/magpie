<div align="center">
  <img src="resources/logo/magpie-light-github.png" alt="Magpie logo" width="180">
  <h3>A multi-user, all-in-one proxy manager</h3>
</div>

<div align="center">
  <img src="https://img.shields.io/github/license/Magpie-Tools/magpie.svg" alt="license">
  <img src="https://img.shields.io/github/issues/Magpie-Tools/magpie.svg" alt="issues">
  <a href="https://discord.gg/7FWAGXzhkC">
    <img src="https://img.shields.io/badge/Discord-%235865F2.svg?&logo=discord&logoColor=white" alt="discord">
  </a>
  <br>
  <a href="https://magpie.tools">
    <img src="https://img.shields.io/badge/Website-magpie.tools-0f766e?style=flat-square&logoColor=white" alt="website">
  </a>
  <a href="https://magpie.tools/docs/">
    <img src="https://img.shields.io/badge/Docs-magpie.tools%2Fdocs-1f2937?style=flat-square&logo=gitbook&logoColor=white" alt="docs">
  </a>
  <br>
  <img src="https://img.shields.io/docker/pulls/kuuchen/magpie-frontend?style=flat-square&logo=docker&label=frontend%20pulls" alt="docker frontend pulls">
  <img src="https://img.shields.io/docker/pulls/kuuchen/magpie-backend?style=flat-square&logo=docker&label=backend%20pulls" alt="docker backend pulls">
</div>

---

Magpie is a self-hosted proxy manager that scrapes public proxy sources,
continuously checks their health, filters bad entries, calculates reputation
scores, and creates rotating proxy endpoints from the healthy pool.

This is the Magpie distribution repository. It connects the independently
versioned frontend and backend images with PostgreSQL and Redis, and owns the
install, update, and performance-validation tooling. Application source code
lives in the component repositories below.

## Repository map

| Repository | Responsibility |
| --- | --- |
| [`Magpie-Tools/magpie`](https://github.com/Magpie-Tools/magpie) | Distribution: Docker Compose, installers, update helpers, release configuration, and performance gates |
| [`Magpie-Tools/magpie-frontend`](https://github.com/Magpie-Tools/magpie-frontend) | Angular application and frontend container image |
| [`Magpie-Tools/magpie-backend`](https://github.com/Magpie-Tools/magpie-backend) | Go API, background jobs, proxy workers, and backend container image |
| [`Magpie-Tools/magpie-website`](https://github.com/Magpie-Tools/magpie-website) | `magpie.tools` marketing website |
| [`Magpie-Tools/magpie-docs`](https://github.com/Magpie-Tools/magpie-docs) | Docusaurus documentation published at `/docs` |

The repositories are siblings, not Git submodules. A production installation
only needs this distribution repository or the one-command installer. It pulls
the published component images.

## Features

- Multi-workspace dashboard and API with owner, admin, operator, and viewer roles
- Workspace-owned capacity, operational settings, managed proxies, tags, sources, judges, and rotators
- Automatic proxy scraping and health checks
- Provider hostname, IPv4, and IPv6 proxy import, checking, search, export, and rotation. IP blacklists apply to literal addresses, and automatic scraping remains IPv4-only.
- Workspace-owned, color-coded proxy tags with multi-tag assignment, import tagging, search, and filtering
- Active, paused, and archived managed-proxy lifecycle; capacity overflow is retained rather than deleted
- Reputation scoring and filters
- User-defined rotating proxy endpoints
- HTTP, HTTPS, SOCKS4, and SOCKS5 application protocols
- TCP and QUIC/HTTP3 transport support

<img src="resources/screenshots/dashboard.png" alt="Magpie dashboard">

<details>
  <summary>More screenshots</summary>
  <img src="resources/screenshots/proxyList.png" alt="Proxy list">
  <img src="resources/screenshots/proxyDetail.png" alt="Proxy details">
  <img src="resources/screenshots/rotatingProxies.png" alt="Rotating proxies">
  <img src="resources/screenshots/accountSettings.png" alt="Account settings">
</details>

## Quick start

Prerequisite: [Docker Desktop](https://www.docker.com/) or Docker Engine with
Docker Compose.

### One-command install

macOS/Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/Magpie-Tools/magpie/refs/heads/master/scripts/install.sh | bash
```

Windows PowerShell:

```powershell
iwr -useb https://raw.githubusercontent.com/Magpie-Tools/magpie/refs/heads/master/scripts/install.ps1 | iex
```

The installer creates a `magpie/` deployment directory, generates `.env`, pulls
the published images, and starts the complete stack.

If Docker reports a socket permission error on Linux, add your user to the
Docker group and start a new login session:

```bash
sudo usermod -aG docker "$USER"
```

### Clone and run

```bash
git clone https://github.com/Magpie-Tools/magpie.git
cd magpie
cp .env.example .env
# Edit .env and replace PROXY_ENCRYPTION_KEY and the example credentials.
docker compose run --rm backend --migrate-only
docker compose up -d
```

Open:

- UI: http://localhost:5050
- API: http://localhost:5656/api
- Documentation: https://magpie.tools/docs/

The first registered user becomes the administrator in the default local
configuration. Optional geolocation and reputation integrations are available
under **Admin → Plugins**.

> [!WARNING]
> Keep `PROXY_ENCRYPTION_KEY` stable across restarts and updates. It encrypts
> proxy usernames and passwords in PostgreSQL and keys route fingerprints.
> Starting the backend with a different key prevents existing secrets from
> being decrypted. For checker throughput, Redis queue payloads contain proxy
> addresses and credentials in plaintext by default. Keep Redis private and
> protect its access, volumes, and backups. Set
> `PROXY_QUEUE_ENCRYPT_CREDENTIALS=true` only if you accept its per-check cost.

For production updates, take coordinated PostgreSQL and Redis backups, stop the
backend, and run the new image's database migration before starting it again:

```bash
docker compose stop backend
docker compose run --rm backend --migrate-only
docker compose up -d
```

Do not skip the migration when updating an existing installation. Workspace-capable
releases create one personal workspace and owner membership for every existing
account, move operational ownership from `user_id` to `workspace_id`, and repair
PostgreSQL foreign keys. Existing resources remain available through the new
default workspace. The migration is not compatible with older backend images;
rollback requires the coordinated PostgreSQL and Redis backups.

API clients may select a workspace with `X-Workspace-ID`. When omitted, the
backend uses the authenticated account's default workspace membership.

The included Compose configuration is intended for local and self-hosted
deployments. Internet-exposed production deployments should harden secrets,
database and Redis access, TLS termination, registration policy, and backups.

## Component image versions

Frontend and backend releases can be selected independently in `.env`:

```dotenv
MAGPIE_BACKEND_IMAGE=kuuchen/magpie-backend
MAGPIE_BACKEND_TAG=latest
MAGPIE_FRONTEND_IMAGE=kuuchen/magpie-frontend
MAGPIE_FRONTEND_TAG=latest
```

Pin immutable release tags for reproducible deployments. The legacy
`MAGPIE_IMAGE_TAG` variable remains supported as a shared fallback; an explicit
component tag takes precedence.

## Updating

For an installer-created deployment, refresh the Compose definition, pull
images, and restart the stack with:

macOS/Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/Magpie-Tools/magpie/refs/heads/master/scripts/update.sh | bash
```

Windows PowerShell:

```powershell
iwr -useb https://raw.githubusercontent.com/Magpie-Tools/magpie/refs/heads/master/scripts/update.ps1 | iex
```

For a cloned distribution repository:

```bash
./scripts/update-stack.sh
```

Or from Windows Command Prompt:

```bat
scripts\update-stack.bat
```

These helpers pull distribution changes and published images; they no longer
build frontend or backend source from this repository.

## Local development

Clone all five repositories as siblings:

```text
workspace/
├── magpie/
├── magpie-backend/
├── magpie-frontend/
├── magpie-website/
└── magpie-docs/
```

Use this repository for shared infrastructure:

```bash
cd magpie
cp .env.example .env
# Edit .env first.
docker compose up -d postgres redis
```

Then run the component you are developing from its own repository:

- Backend: `cd ../magpie-backend && go run ./cmd/magpie`
- Frontend: `cd ../magpie-frontend && npm ci && npm run start`
- Website: `cd ../magpie-website && npm ci && npm run dev`
- Docs: `cd ../magpie-docs && npm ci && npm run start`

The backend must be configured to use PostgreSQL at `localhost:5434` and Redis
at `localhost:8946` when those services are started from this Compose file.
Each component repository contains its own build, test, and development details.

The performance release gate remains in [`scripts/perf`](scripts/perf).

## Publishing the website and documentation

The public site is assembled on this repository's `gh-pages` branch. With all
five repositories cloned as siblings, publish the website and documentation in
sequence:

```bash
cd ../magpie-website
npm run deploy

cd ../magpie-docs
npm run deploy
```

Both component scripts build their source, then call
[`scripts/publish-pages-artifact.sh`](scripts/publish-pages-artifact.sh) in this
repository. The website replaces the branch root while preserving `/docs`; the
documentation deployment replaces only `/docs`.

Set `MAGPIE_DISTRIBUTION_REPO` if this repository is not at `../magpie`. Set
`MAGPIE_DEPLOY_DRY_RUN=1` to build and validate without changing `gh-pages`, or
`MAGPIE_DEPLOY_PUSH=0` to create the local `gh-pages` commit without pushing it.
Run the two deployments sequentially so each starts from the latest branch.

## Attributions and community

- The [AbuseIPDB](https://www.abuseipdb.com/) logo is used with permission when linking to its site
- Website: https://magpie.tools
- Docs: https://magpie.tools/docs/
- Discord: https://discord.gg/7FWAGXzhkC

## License

Magpie is distributed under the GNU Affero General Public License v3.0. See
[`LICENSE`](LICENSE) for the complete license.
