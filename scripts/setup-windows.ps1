# Requires: Windows 10/11, internet, and (for login) Google Chrome.
$ErrorActionPreference = "Stop"
Set-Location (Split-Path -Parent $PSScriptRoot)

function Add-GoToPath {
    $candidates = @(
        "$env:ProgramFiles\Go\bin",
        "$env:USERPROFILE\go\bin",
        "$env:USERPROFILE\sdk\go\bin"
    )
    foreach ($dir in $candidates) {
        if (Test-Path (Join-Path $dir "go.exe")) {
            $env:Path = "$dir;$env:Path"
        }
    }
}

function Has-Command($name) {
    return [bool](Get-Command $name -ErrorAction SilentlyContinue)
}

Write-Host "Tumblr bot — laptop setup" -ForegroundColor Cyan
Write-Host ""

Add-GoToPath

if (-not (Has-Command "go")) {
    Write-Host "Go is not installed."
    if (Has-Command "winget") {
        Write-Host "Installing Go with winget..."
        winget install --id GoLang.Go -e --accept-package-agreements --accept-source-agreements
        Add-GoToPath
        $machinePath = [Environment]::GetEnvironmentVariable("Path", "Machine")
        $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
        if ($machinePath) { $env:Path = "$machinePath;$userPath;$env:Path" }
        Add-GoToPath
    }
    if (-not (Has-Command "go")) {
        Write-Host "Install Go from https://go.dev/dl/ then run this setup again."
        exit 1
    }
}

Write-Host "Go: $(go version)"

$chrome = @(
    "$env:ProgramFiles\Google\Chrome\Application\chrome.exe",
    "${env:ProgramFiles(x86)}\Google\Chrome\Application\chrome.exe",
    "$env:LOCALAPPDATA\Google\Chrome\Application\chrome.exe"
) | Where-Object { Test-Path $_ } | Select-Object -First 1

if ($chrome) {
    Write-Host "Chrome: $chrome"
} else {
    Write-Host "Google Chrome was not found. Login (-login) needs Chrome."
    Write-Host "Install it from https://www.google.com/chrome/ then continue."
}

New-Item -ItemType Directory -Force -Path "pics\posted", "data", "posts", "scraped" | Out-Null
if (-not (Test-Path "posts\queue.txt")) {
    New-Item -ItemType File -Path "posts\queue.txt" | Out-Null
}
if (-not (Test-Path "config\auth.json")) {
    Copy-Item "config\auth.example.json" "config\auth.json"
    Write-Host "Created config\auth.json (blog name is filled in after login)."
}

Write-Host "Downloading modules and building tumblr.exe..."
go mod download
go build -o tumblr.exe .
Write-Host ""
Write-Host "Build OK: tumblr.exe" -ForegroundColor Green
Write-Host ""
Write-Host "Next:"
Write-Host "  1. Double-click login.bat  (log into Tumblr in the NEW Chrome window, then press Enter)"
Write-Host "  2. Double-click run.bat    (opens the GUI)"
