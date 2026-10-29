<div align="center">
  <img src="resources/logo/magpie-light-github.png" alt="Magpie logo" width="180">
  <h3>Self-hosted proxy management for you and your team</h3>
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

Magpie collects proxies, checks which ones work, and serves them through stable
rotating endpoints. Keep a usable proxy pool for your applications without
manually sorting through stale lists and replacing dead proxies.

Run it on your own infrastructure with Docker Compose, manage it in the web
dashboard, and connect your applications through the API or a rotating proxy.

## Features

- Collects proxies automatically from web sources and supports imports of proxy lists and provider endpoints.
- Continuously checks HTTP, HTTPS, SOCKS4, and/or SOCKS5 proxies, with checker rules for different tags.
- Provides rotating proxy endpoints that select healthy upstream proxies.
- Tracks proxy health, latency, uptime, and reputation.
- Supports tags, search, filters, and exports for provider hostnames, IPv4, and IPv6 proxies.
- Supports shared workspaces with roles and invitations.
- Sends workspace and pool-health alerts by email, Slack, Discord, or webhook.

Proxy network types are estimates based on local GeoLite data and cached reverse
DNS. Known hosting networks keep their datacenter classification; generic words
such as `ip` or `static` do not imply residential access. Existing labels update
during background geo refresh. See [estimated proxy types](https://magpie.tools/docs/user-guide/proxies#estimated-proxy-type)
for the classification rules and limitations.

<img src="resources/screenshots/dashboard.png" alt="Magpie dashboard">

<details>
  <summary>More screenshots</summary>
  <img src="resources/screenshots/proxyList.png" alt="Proxy list">
  <img src="resources/screenshots/proxyDetail.png" alt="Proxy details">
  <img src="resources/screenshots/rotatingProxies.png" alt="Rotating proxies">
  <img src="resources/screenshots/accountSettings.png" alt="Account settings">
</details>

## Install

Magpie requires either [Docker Desktop](https://www.docker.com/) or the Docker Engine with
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

The installer asks for a proxy encryption key, creates a `magpie/` deployment
directory, generates the remaining credentials, and starts the stack.

### Clone and run

```bash
git clone https://github.com/Magpie-Tools/magpie.git
cd magpie
cp .env.example .env
```

Edit `.env` and replace the example `PROXY_ENCRYPTION_KEY` and database password.
Then migrate and start:

```bash
docker compose run --rm backend --migrate-only && docker compose up -d
```

Open [http://localhost:5050](http://localhost:5050) and register your first
account. The first registered account becomes the administrator in the default
local configuration.

Keep `PROXY_ENCRYPTION_KEY` unchanged across restarts and upgrades. Existing
encrypted credentials depend on it. Redis queue credentials are plaintext by
default, so keep Redis private. See the [installation guide](https://magpie.tools/docs/getting-started/installation)
for configuration and the [deployment guide](https://magpie.tools/docs/operations/deployment)
before exposing Magpie on a public server.

## Upgrade

Take a [coordinated backup](https://magpie.tools/docs/operations/backup-and-recovery)
of PostgreSQL, Redis, configuration, and keys before upgrading. Keep
`PROXY_ENCRYPTION_KEY` unchanged and update the frontend and backend together.

For an installer-created deployment, run the update command from the directory
containing `magpie/`:

macOS/Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/Magpie-Tools/magpie/refs/heads/master/scripts/update.sh | bash
```

Windows PowerShell:

```powershell
iwr -useb https://raw.githubusercontent.com/Magpie-Tools/magpie/refs/heads/master/scripts/update.ps1 | iex
```

For a cloned repository, run this from its root:

```bash
./scripts/update-stack.sh
```

Or in Windows Command Prompt:

```bat
scripts\update-stack.bat
```

The helpers pull updated images, stop the backend, run database migrations, and
restart after the migration succeeds. Stop any additional backend replicas
before running the helper.

See the [upgrade guide](https://magpie.tools/docs/getting-started/updating) for
image versions, production deployments, migration notes, and rollback.

## Documentation

Start with [your first proxy](https://magpie.tools/docs/learn/first-proxy), then
use the guides for [rotating proxies](https://magpie.tools/docs/user-guide/rotating-proxies),
[checker settings and tag rules](https://magpie.tools/docs/user-guide/checker-and-judges),
and [alerts](https://magpie.tools/docs/user-guide/alerts). The
[full documentation](https://magpie.tools/docs/) also covers the API,
configuration, security, and troubleshooting.

## Repository map

| Repository | Content |
| --- | --- |
| [magpie](https://github.com/Magpie-Tools/magpie) | Docker Compose, installation, updates, and release validation |
| [magpie-backend](https://github.com/Magpie-Tools/magpie-backend) | Go API, checker, scraper, background jobs, and rotating proxy listeners |
| [magpie-frontend](https://github.com/Magpie-Tools/magpie-frontend) | Angular web application |
| [magpie-docs](https://github.com/Magpie-Tools/magpie-docs) | User guides, API reference, and operations documentation |
| [magpie-website](https://github.com/Magpie-Tools/magpie-website) | Public website at magpie.tools |

## Community and contributing

Join [Discord](https://discord.gg/7FWAGXzhkC) for discussion or
[open an issue](https://github.com/Magpie-Tools/magpie/issues) to report a bug.

See the [contributing guide](https://magpie.tools/docs/contributing/contributing)
for local development and validation.

## License

Magpie is licensed under the GNU Affero General Public License v3.0.
See [LICENSE](LICENSE).

The [AbuseIPDB](https://www.abuseipdb.com/) logo is used with permission.
