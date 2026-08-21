$ErrorActionPreference = "Stop"

$repoOwner = if ($env:MAGPIE_REPO_OWNER) { $env:MAGPIE_REPO_OWNER } else { "Magpie-Tools" }
$repoName  = if ($env:MAGPIE_REPO_NAME)  { $env:MAGPIE_REPO_NAME }  else { "magpie" }
$repoRef   = if ($env:MAGPIE_REPO_REF)   { $env:MAGPIE_REPO_REF }   else { "master" }
$repoRefPath = if ($repoRef -like "refs/*") { $repoRef } else { "refs/heads/$repoRef" }

$installDir = if ($env:MAGPIE_INSTALL_DIR) { $env:MAGPIE_INSTALL_DIR } else { "magpie" }

if ([string]::IsNullOrWhiteSpace($installDir) -or $installDir -eq "\") {
  throw "MAGPIE_INSTALL_DIR must not be empty or '\'."
}

if (-not (Test-Path -LiteralPath $installDir)) {
  throw "Install directory not found: $installDir. Run the installer first or set MAGPIE_INSTALL_DIR."
}

$composeUrl = if ($env:MAGPIE_COMPOSE_URL) {
  $env:MAGPIE_COMPOSE_URL
} else {
  "https://raw.githubusercontent.com/$repoOwner/$repoName/$repoRefPath/docker-compose.yml"
}

$envExampleUrl = if ($env:MAGPIE_ENV_EXAMPLE_URL) {
  $env:MAGPIE_ENV_EXAMPLE_URL
} else {
  "https://raw.githubusercontent.com/$repoOwner/$repoName/$repoRefPath/.env.example"
}

function Test-Command([string]$name) {
  return $null -ne (Get-Command $name -ErrorAction SilentlyContinue)
}

if (-not (Test-Command "docker")) {
  throw "Docker is required but was not found in PATH. Install Docker Desktop."
}

function Invoke-NativeCommand {
  param(
    [Parameter(Mandatory)][string]$FilePath,
    [Parameter()][string[]]$Arguments = @(),
    [switch]$Quiet
  )

  $oldPreference = $ErrorActionPreference
  $ErrorActionPreference = "Continue"
  try {
    if ($Quiet) {
      & $FilePath @Arguments 1>$null 2>$null
    } else {
      & $FilePath @Arguments
    }
    return $LASTEXITCODE
  } finally {
    $ErrorActionPreference = $oldPreference
  }
}

function Invoke-NativeOrThrow {
  param(
    [Parameter(Mandatory)][string]$FilePath,
    [Parameter()][string[]]$Arguments = @(),
    [Parameter(Mandatory)][string]$What
  )

  $exitCode = Invoke-NativeCommand -FilePath $FilePath -Arguments $Arguments
  if ($exitCode -ne 0) {
    throw "$What failed (exit code $exitCode)."
  }
}

function Get-DockerInfoText {
  $oldPreference = $ErrorActionPreference
  $ErrorActionPreference = "Continue"
  try {
    $text = (& cmd /d /c "docker info 2>&1" | Out-String).Trim()
    $exitCode = $LASTEXITCODE
    return @{ ExitCode = $exitCode; Text = $text }
  } finally {
    $ErrorActionPreference = $oldPreference
  }
}

function Get-ComposeCommand {
  $exitCode = Invoke-NativeCommand -FilePath "docker" -Arguments @("compose", "version") -Quiet
  if ($exitCode -eq 0) {
    return @{ FilePath = "docker"; BaseArgs = @("compose") }
  }
  if (Test-Command "docker-compose") {
    return @{ FilePath = "docker-compose"; BaseArgs = @() }
  }
  throw "Docker Compose is required but was not found. Install Docker Desktop or docker-compose."
}

$compose = Get-ComposeCommand
$composeFile = $compose.FilePath
$composeBaseArgs = $compose.BaseArgs

if ((Invoke-NativeCommand -FilePath "docker" -Arguments @("info") -Quiet) -ne 0) {
  $details = (Get-DockerInfoText).Text
  $message = "Docker daemon not reachable from this PowerShell session."
  if (-not [string]::IsNullOrWhiteSpace($details)) {
    $message += "`n`nDocker output:`n$details"
  }
  $message += "`n`nTips:`n- Ensure Docker Desktop shows 'Docker is running'`n- Try: docker context ls; docker context use default`n- If you're using WSL, ensure WSL integration is enabled"
  throw $message
}

Set-Location -LiteralPath $installDir

$requiredSecrets = @("PROXY_ENCRYPTION_KEY")

if (Test-Path -LiteralPath ".env") {
  $envContent = Get-Content -LiteralPath ".env" -Raw
  foreach ($secretName in $requiredSecrets) {
    $hasSecretInDotenv = $envContent -match "(?m)^$secretName="
    $hasSecretInProcess = -not [string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($secretName))
    if (-not $hasSecretInDotenv -and -not $hasSecretInProcess) {
      throw "Missing $secretName in $installDir\\.env. Add $secretName to .env or set it in your environment and rerun."
    }
  }
} else {
  $missing = @()
  foreach ($secretName in $requiredSecrets) {
    if ([string]::IsNullOrWhiteSpace([Environment]::GetEnvironmentVariable($secretName))) {
      $missing += $secretName
    }
  }

  if ($missing.Count -gt 0) {
    throw "Missing $installDir\\.env (required secrets: $($missing -join ', ')). Restore it or set $($missing -join ', ') and rerun."
  }
}

function Download-File([string]$url, [string]$dest) {
  $params = @{ Uri = $url; OutFile = $dest }
  if ((Get-Command Invoke-WebRequest).Parameters.ContainsKey("UseBasicParsing")) {
    $params.UseBasicParsing = $true
  }
  Invoke-WebRequest @params
}

Write-Host "Downloading latest docker-compose.yml..."
$tmpCompose = "docker-compose.yml.new.$PID"
Download-File $composeUrl $tmpCompose

if (Test-Path -LiteralPath "docker-compose.yml") {
  Copy-Item -LiteralPath "docker-compose.yml" -Destination "docker-compose.yml.bak" -Force
}
Move-Item -LiteralPath $tmpCompose -Destination "docker-compose.yml" -Force

Write-Host "Refreshing .env.example (optional)..."
try {
  Download-File $envExampleUrl ".env.example"
} catch {
  # best-effort
}

Write-Host "Pulling images..."
Invoke-NativeOrThrow -FilePath $composeFile -Arguments @($composeBaseArgs + @("-f", "docker-compose.yml", "pull")) -What "docker compose pull"

Write-Host "Stopping backend for database migration..."
Invoke-NativeOrThrow -FilePath $composeFile -Arguments @($composeBaseArgs + @("-f", "docker-compose.yml", "stop", "backend")) -What "docker compose stop backend"

Write-Host "Running database migration..."
Invoke-NativeOrThrow -FilePath $composeFile -Arguments @($composeBaseArgs + @("-f", "docker-compose.yml", "run", "--rm", "backend", "--migrate-only")) -What "backend database migration"

Write-Host "Applying update..."
Invoke-NativeOrThrow -FilePath $composeFile -Arguments @($composeBaseArgs + @("-f", "docker-compose.yml", "up", "-d")) -What "docker compose up"

Write-Host "Done."
