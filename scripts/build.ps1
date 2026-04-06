# Duman Build Script for Windows
# Usage: .\scripts\build.ps1 [-Version "0.1.0"]

param(
    [string]$Version = "0.1.0"
)

$ErrorActionPreference = "Stop"

$Commit = try { git rev-parse --short HEAD 2>$null } catch { "unknown" }
$LdFlags = "-s -w -X main.version=$Version -X main.commit=$Commit"
$OutDir = "dist"

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

Write-Host "=== Duman v$Version ($Commit) ===" -ForegroundColor Cyan
Write-Host ""

$platforms = @(
    @{ OS = "windows"; Arch = "amd64"; Ext = ".exe" },
    @{ OS = "linux";   Arch = "amd64"; Ext = "" },
    @{ OS = "linux";   Arch = "arm64"; Ext = "" },
    @{ OS = "darwin";  Arch = "amd64"; Ext = "" },
    @{ OS = "darwin";  Arch = "arm64"; Ext = "" }
)

foreach ($p in $platforms) {
    foreach ($bin in @("duman-relay", "duman-client")) {
        $out = "$OutDir/$bin-$($p.OS)-$($p.Arch)$($p.Ext)"
        Write-Host "  Building $out..." -ForegroundColor Gray
        $env:GOOS = $p.OS
        $env:GOARCH = $p.Arch
        go build -ldflags $LdFlags -o $out "./cmd/$bin"
        if ($LASTEXITCODE -ne 0) {
            Write-Host "FAILED: $out" -ForegroundColor Red
            exit 1
        }
    }
}

# Reset environment
Remove-Item Env:GOOS -ErrorAction SilentlyContinue
Remove-Item Env:GOARCH -ErrorAction SilentlyContinue

Write-Host ""
Write-Host "=== Build complete ===" -ForegroundColor Green
Get-ChildItem "$OutDir/duman-*" | Format-Table Name, Length
