<div align="center">
  <img src="frontend/src/assets/logo/magpie-logo-thumbnail.png" alt="Magpie logo">
  <h3>A Multi-user AIO Proxy Manager</h3>
</div>

<div align="center">
  <img src="https://img.shields.io/github/license/Kuucheen/magpie.svg" alt="license">
  <img src="https://img.shields.io/github/issues/Kuucheen/magpie.svg" alt="issues">
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


[//]: # (  <img src="https://img.shields.io/github/stars/Kuucheen/magpie.svg?style=social" alt="stars">)
</div>

---

Magpie is a self-hosted proxy manager that turns messy proxy lists into something you can actually use: 
- it scrapes proxies from public sources
- continuously checks which ones are alive
- filters out dead/bad entries
- assigns each proxy a reputation score (uptime/latency/anonymity)
- lets you create your own rotating proxy endpoints from the healthy pool

all via a web dashboard.

<img src="resources/screenshots/dashboard.png" alt="Dashboard">


<details>
    <summary>More Screenshots</summary>
    <img src="resources/screenshots/proxyList.png" alt="Proxy List">
    <img src="resources/screenshots/proxyDetail.png" alt="Proxy Details">
    <img src="resources/screenshots/rotatingProxies.png" alt="Rotating Proxies">
    <img src="resources/screenshots/accountSettings.png" alt="Account Settings">
</details>

## Features
- Multi-user
- Auto-scraping
- Proxy Checking / Health checks
- Reputation & filters
- Rotating proxy endpoints
- Dashboard + API
- Application protocols support (HTTP, HTTPS, SOCKS4, SOCKS5)
- Transport protocols support (TCP, QUIC/HTTP3)

## Quick Start

1. **Install Prerequisites:**
    - [Docker Desktop](https://www.docker.com/) (or Docker Engine + Compose)

2. **One-command install (recommended)**

   **macOS/Linux**:
   ```bash
   curl -fsSL https://raw.githubusercontent.com/Kuucheen/magpie/refs/heads/master/scripts/install.sh | bash
   ```
   If you see a Docker socket permission error on Linux, the installer will try to use `sudo` for Docker commands (you may be prompted).  
   Alternative fix: `sudo usermod -aG docker "$USER"` (then log out/in, or run `newgrp docker`).  
   Note: `sudo curl ... | bash` still runs `bash` as your user. Try `curl ... | sudo bash` instead.

   **Windows (PowerShell)**:
   ```bash
   iwr -useb https://raw.githubusercontent.com/Kuucheen/magpie/refs/heads/master/scripts/install.ps1 | iex
   ```

   This creates a `magpie/` folder with a `docker-compose.yml` and `.env`, then starts the stack.

3. **Required secrets** – Magpie requires:
   - `PROXY_ENCRYPTION_KEY` to encrypt stored proxy secrets (keep it stable between restarts/updates)

> [!WARNING]
> `PROXY_ENCRYPTION_KEY` locks all stored secrets (proxy auth, passwords, and ip addresses).  
> If you start the backend (or update to a new version) with a *different* key than the one used before, decryption fails and previously added proxies will not display or validate.  
> **Fix:** start the backend again using the **previous key** and everything works like before.  
> **Only rotate on purpose:** if you need a new key, export your proxies first.

4. **If you don't want to use the installer** 

    Requires [Git](https://git-scm.com/downloads)

   ```bash
   git clone https://github.com/Kuucheen/magpie.git
   cd magpie
   cp .env.example .env
   # edit .env and set PROXY_ENCRYPTION_KEY
   # optional: override JWT_SECRET
   # optional: override DB_USERNAME/DB_PASSWORD/DB_NAME
   # optional: configure MAIL_FROM_ADDRESS/SMTP_HOST for email delivery
   docker compose up -d
   ```
   > `docker-compose.yml` is local/dev oriented. For production deployments, use hardened manifests and secure DB/Redis/TLS settings.
5. **Dive in**
    - UI: http://localhost:5050
    - API: http://localhost:5656/api
    - Docs: https://magpie.tools/docs/

      The first user who registers becomes admin automatically.

Optional integrations, including geolocation and advanced reputation checks, can be enabled and configured under Admin -> Plugins.

### Updating
Use the helper scripts that match how you installed Magpie.

- **If you used the one-command installer**:
  - Refreshes `docker-compose.yml`, updates image references, pulls the latest images, and restarts the stack.
  - **macOS/Linux**: 
      ```bash
      curl -fsSL https://raw.githubusercontent.com/Kuucheen/magpie/refs/heads/master/scripts/update.sh | bash
      ```
      If you see a Docker socket permission error on Linux, the updater will try to use `sudo` for Docker commands (you may be prompted).
  - **Windows (PowerShell)**: 
      ```bash
      iwr -useb https://raw.githubusercontent.com/Kuucheen/magpie/refs/heads/master/scripts/update.ps1 | iex
      ```

- **If you cloned the project**:
  - Pulls the latest repo changes and rebuilds the `frontend` and `backend` containers.
  - **macOS/Linux**:
    ```bash
    ./scripts/update-frontend-backend.sh
    ```
  - **Windows (Command Prompt)**:
    ```bash
    scripts\update-frontend-backend.bat
    ```
    Double-click the file or run it from the repo root.

## Local Development
- Prerequisites:
  - Go `1.26.x`
  - Node.js `20.19+` or `22.12+`
  - npm
  - Docker
- Services: `docker compose up -d postgres redis`
- Backend: `cd backend && go run ./cmd/magpie`
- Frontend: `cd frontend && npm install && npm run start`
- Docs site: `cd website/docs && npm install && npm run start`

Optional email delivery env vars for the backend:
- `MAIL_FROM_ADDRESS`: sender address used in outbound email, for example `no-reply@example.com`
- `MAIL_FROM_NAME`: optional display name, for example `Magpie`
- `PUBLIC_APP_URL`: public frontend base URL used in password reset links, for example `https://magpie.example.com`
- `SMTP_HOST`: SMTP server host
- `SMTP_PORT`: SMTP server port, defaults to `587`
- `SMTP_USERNAME` and `SMTP_PASSWORD`: optional SMTP auth credentials; if one is set, both must be set
- `PASSWORD_RESET_TOKEN_TTL_MINUTES`: optional reset-link lifetime, defaults to `30`

## Attributions & External Sources
- [AbuseIPDB](https://www.abuseipdb.com/) — logo used with permission when linking to their site.

## Community
- Website: https://magpie.tools
- Docs: https://magpie.tools/docs/
- Discord: https://discord.gg/7FWAGXzhkC
- Issues & feature requests: open them on GitHub.

## License
Magpie ships under the **GNU Affero General Public License v3.0**. See `LICENSE` for the full text. Contributions are more than welcome.
