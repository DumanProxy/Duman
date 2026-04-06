#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Duman Client Installer for Windows
.DESCRIPTION
    Installs duman-client.exe, creates config directory, and generates initial config.
.PARAMETER RelayAddress
    The relay server address (e.g., "myserver.com:5432")
.PARAMETER SharedSecret
    The shared secret from the relay installation
.PARAMETER Password
    The PostgreSQL auth password from the relay config
.EXAMPLE
    .\install-client.ps1 -RelayAddress "192.168.1.100:5432" -SharedSecret "abc123..." -Password "xxx"
.EXAMPLE
    .\install-client.ps1   # Interactive mode — will prompt for values
#>

param(
    [string]$RelayAddress = "",
    [string]$SharedSecret = "",
    [string]$Password = "",
    [string]$Username = "sensor_writer",
    [string]$FromLocal = ""
)

$ErrorActionPreference = "Stop"
$Version = "0.1.0"

# --- Paths ---
$InstallDir = "$env:ProgramFiles\Duman"
$ConfigDir  = "$env:APPDATA\Duman"
$BinaryName = "duman-client.exe"
$BinaryPath = "$InstallDir\$BinaryName"
$ConfigPath = "$ConfigDir\client.yaml"

# --- Colors ---
function Write-Info  { Write-Host "[INFO]  $args" -ForegroundColor Cyan }
function Write-OK    { Write-Host "[OK]    $args" -ForegroundColor Green }
function Write-Warn  { Write-Host "[WARN]  $args" -ForegroundColor Yellow }

# --- Banner ---
Write-Host ""
Write-Host "======================================" -ForegroundColor Cyan
Write-Host "   Duman Client Installer v$Version   " -ForegroundColor Cyan
Write-Host "======================================" -ForegroundColor Cyan
Write-Host ""

# --- Interactive prompts ---
if (-not $RelayAddress) {
    $RelayAddress = Read-Host "Relay server address (e.g., myserver.com:5432)"
    if (-not $RelayAddress) {
        Write-Host "Relay address is required." -ForegroundColor Red
        exit 1
    }
}

if (-not $SharedSecret) {
    $SharedSecret = Read-Host "Shared secret (from relay installation)"
    if (-not $SharedSecret) {
        Write-Host "Shared secret is required." -ForegroundColor Red
        exit 1
    }
}

if (-not $Password) {
    $Password = Read-Host "Auth password (from relay config, sensor_writer password)"
    if (-not $Password) {
        Write-Host "Password is required." -ForegroundColor Red
        exit 1
    }
}

# --- Install binary ---
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path $ConfigDir | Out-Null

if ($FromLocal -and (Test-Path $FromLocal)) {
    Write-Info "Installing from local binary: $FromLocal"
    Copy-Item $FromLocal -Destination $BinaryPath -Force
} elseif (Test-Path "dist\duman-client-windows-amd64.exe") {
    Write-Info "Installing from dist\duman-client-windows-amd64.exe"
    Copy-Item "dist\duman-client-windows-amd64.exe" -Destination $BinaryPath -Force
} elseif (Test-Path "bin\duman-client-windows-amd64.exe") {
    Write-Info "Installing from bin\duman-client-windows-amd64.exe"
    Copy-Item "bin\duman-client-windows-amd64.exe" -Destination $BinaryPath -Force
} else {
    $DownloadUrl = "https://github.com/dumanproxy/duman/releases/download/v$Version/duman-client-windows-amd64.exe"
    Write-Info "Downloading duman-client v$Version..."
    try {
        Invoke-WebRequest -Uri $DownloadUrl -OutFile $BinaryPath -UseBasicParsing
    } catch {
        Write-Host "Download failed. Place duman-client-windows-amd64.exe in dist\ and retry." -ForegroundColor Red
        exit 1
    }
}

Write-OK "Binary installed: $BinaryPath"

# --- Verify ---
& $BinaryPath --version

# --- Add to PATH ---
$machinePath = [Environment]::GetEnvironmentVariable("PATH", "Machine")
if ($machinePath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("PATH", "$machinePath;$InstallDir", "Machine")
    $env:PATH = "$env:PATH;$InstallDir"
    Write-OK "Added $InstallDir to system PATH"
} else {
    Write-OK "$InstallDir already in PATH"
}

# --- Generate config ---
if (-not (Test-Path $ConfigPath)) {
    $seed = Get-Random -Minimum 1 -Maximum 99999
    $configContent = @"
# Duman Client Configuration - v$Version
# Generated on $(Get-Date -Format "yyyy-MM-ddTHH:mm:ss")

proxy:
  listen: "127.0.0.1:1080"
  mode: "socks5"

tunnel:
  shared_secret: "$SharedSecret"
  chunk_size: 16384
  response_mode: "poll"
  cipher: "auto"

relays:
  - address: "$RelayAddress"
    protocol: "postgresql"
    weight: 10
    database: "analytics"
    username: "$Username"
    password: "$Password"

scenario: "ecommerce"

schema:
  mode: "template"
  mutate: true
  seed: $seed

log:
  level: "info"
  format: "text"
  output: "stderr"
"@
    Set-Content -Path $ConfigPath -Value $configContent -Encoding UTF8
    Write-OK "Config created: $ConfigPath"
} else {
    Write-Warn "Config already exists: $ConfigPath (not overwritten)"
}

# --- Create start script ---
$startScript = @"
@echo off
echo Starting Duman Client...
echo SOCKS5 proxy will be available at 127.0.0.1:1080
echo.
echo Configure your browser/app to use SOCKS5 proxy: 127.0.0.1:1080
echo Press Ctrl+C to stop.
echo.
"$BinaryPath" -c "$ConfigPath" -v
pause
"@

Set-Content -Path "$InstallDir\start-duman.bat" -Value $startScript -Encoding ASCII
Write-OK "Start script: $InstallDir\start-duman.bat"

# --- Create Windows Task (optional auto-start) ---
$createTask = Read-Host "Create startup task to run on login? (y/N)"
if ($createTask -eq "y" -or $createTask -eq "Y") {
    $action = New-ScheduledTaskAction -Execute $BinaryPath -Argument "-c `"$ConfigPath`""
    $trigger = New-ScheduledTaskTrigger -AtLogon
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
    Register-ScheduledTask -TaskName "DumanClient" -Action $action -Trigger $trigger -Settings $settings -Description "Duman tunnel client" -Force | Out-Null
    Write-OK "Scheduled task 'DumanClient' created (runs at login)"
}

# --- Summary ---
Write-Host ""
Write-Host "======================================" -ForegroundColor Green
Write-Host "   Installation Complete!              " -ForegroundColor Green
Write-Host "======================================" -ForegroundColor Green
Write-Host ""
Write-Host "  Binary:  $BinaryPath"
Write-Host "  Config:  $ConfigPath"
Write-Host "  SOCKS5:  127.0.0.1:1080"
Write-Host ""
Write-Host "  Quick Start:" -ForegroundColor Yellow
Write-Host "    duman-client -c `"$ConfigPath`" -v"
Write-Host ""
Write-Host "  Or double-click:" -ForegroundColor Yellow
Write-Host "    $InstallDir\start-duman.bat"
Write-Host ""
Write-Host "  Browser proxy setup:" -ForegroundColor Yellow
Write-Host "    SOCKS5 Host: 127.0.0.1  Port: 1080"
Write-Host ""
Write-Host "  Test connection:" -ForegroundColor Yellow
Write-Host "    curl --socks5 127.0.0.1:1080 https://ifconfig.me"
Write-Host ""
