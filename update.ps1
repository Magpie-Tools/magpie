param([switch]$LocalConfig)

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

$productionComposeUrl = if ($env:MAGPIE_PRODUCTION_COMPOSE_URL) {
  $env:MAGPIE_PRODUCTION_COMPOSE_URL
} else {
  "https://raw.githubusercontent.com/$repoOwner/$repoName/$repoRefPath/docker-compose.production.yml"
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
      & $FilePath @Arguments | Out-Host
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

# Discover through Docker rather than the project inferred from the base file.
# An overlay's name:, -p or COMPOSE_PROJECT_NAME may have selected another name.
$installPath = (Get-Location).ProviderPath
$pathComparison = if ([IO.Path]::DirectorySeparatorChar -eq '\') { [StringComparison]::OrdinalIgnoreCase } else { [StringComparison]::Ordinal }
$backendIds = @(& docker ps -a -q --filter "label=com.docker.compose.service=backend")
if ($LASTEXITCODE -ne 0) {
  throw "Cannot find the existing backend configuration. Update aborted."
}
$selectedFiles = $null
$selectedProject = $null
$selectedDirectory = $null
foreach ($backendId in $backendIds) {
  # A quote-free template also works with Windows PowerShell's native argument
  # passing. Read only labels rather than inspecting secret environment values.
  $labelsJson = (& docker inspect --format '{{json .Config.Labels}}' $backendId | Out-String).Trim()
  if ($LASTEXITCODE -ne 0) {
    throw "Cannot inspect the existing backend. Update aborted."
  }
  $labels = $labelsJson | ConvertFrom-Json
  if ($labels.'com.docker.compose.oneoff' -eq 'True') { continue }
  $files = $labels.'com.docker.compose.project.config_files'
  $project = $labels.'com.docker.compose.project'
  $directory = $labels.'com.docker.compose.project.working_dir'
  $belongsToInstall = -not [string]::IsNullOrWhiteSpace($directory) -and [string]::Equals($directory, $installPath, $pathComparison)
  foreach ($file in @($files -split ',')) {
    if ([string]::Equals($file, (Join-Path $installPath 'docker-compose.yml'), $pathComparison)) {
      $belongsToInstall = $true
    }
  }
  if (-not $belongsToInstall) { continue }
  if ([string]::IsNullOrWhiteSpace($files) -or [string]::IsNullOrWhiteSpace($project) -or
      -not (Test-Path -LiteralPath $directory -PathType Container)) {
    throw "Cannot determine the existing backend's Compose project and files. Update aborted."
  }
  if ($null -ne $selectedFiles -and ($selectedFiles -cne $files -or $selectedProject -cne $project -or $selectedDirectory -cne $directory)) {
    throw "Multiple Compose configurations match this installation. Update aborted."
  }
  $selectedFiles = $files
  $selectedProject = $project
  $selectedDirectory = $directory
}
$composeFileArgs = @()
if ($null -ne $selectedFiles) {
  $composeFileArgs += @("-p", $selectedProject, "--project-directory", $selectedDirectory)
  foreach ($file in $selectedFiles.Split(',')) {
    if (-not (Test-Path -LiteralPath $file -PathType Leaf)) {
      throw "Existing Compose file is missing: $file. Update aborted."
    }
    $composeFileArgs += @("-f", $file)
  }
  Write-Host "Preserving Compose project $selectedProject and files: $selectedFiles"
}
$composeUpdateArgs = @($composeBaseArgs + $composeFileArgs)

if (-not $LocalConfig) {
  Write-Host "Downloading latest docker-compose.yml..."
  $tmpCompose = "docker-compose.yml.new.$PID"
  Download-File $composeUrl $tmpCompose

  if (Test-Path -LiteralPath "docker-compose.yml") {
    Copy-Item -LiteralPath "docker-compose.yml" -Destination "docker-compose.yml.bak" -Force
  }
  Move-Item -LiteralPath $tmpCompose -Destination "docker-compose.yml" -Force

  Write-Host "Downloading production Compose configuration..."
  $tmpProductionCompose = "docker-compose.production.yml.new.$PID"
  Download-File $productionComposeUrl $tmpProductionCompose
  Move-Item -LiteralPath $tmpProductionCompose -Destination "docker-compose.production.yml" -Force

  Write-Host "Refreshing .env.example (optional)..."
  try {
    Download-File $envExampleUrl ".env.example"
  } catch {
    # best-effort
  }
}

Invoke-NativeOrThrow -FilePath $composeFile -Arguments @($composeUpdateArgs + @("config", "--quiet")) -What "Compose configuration validation"

Write-Host "Pulling images..."
Invoke-NativeOrThrow -FilePath $composeFile -Arguments @($composeUpdateArgs + @("pull")) -What "docker compose pull"

Write-Host "Migration safety: confirm you have coordinated PostgreSQL and Redis backups."
Write-Host "Workspace migrations change resource ownership columns; rollback requires those backups."
Write-Host "Stopping backend for database migration..."
Invoke-NativeOrThrow -FilePath $composeFile -Arguments @($composeUpdateArgs + @("stop", "backend")) -What "docker compose stop backend"

Write-Host "Running database migration..."
Invoke-NativeOrThrow -FilePath $composeFile -Arguments @($composeUpdateArgs + @("run", "--rm", "backend", "--migrate-only")) -What "backend database migration"

Write-Host "Applying update..."
$upArgs = @("up", "-d")
if ($LocalConfig) { $upArgs += "--remove-orphans" }
Invoke-NativeOrThrow -FilePath $composeFile -Arguments @($composeUpdateArgs + $upArgs) -What "docker compose up"

Write-Host "Done."
