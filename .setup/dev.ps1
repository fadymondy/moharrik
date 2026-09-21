<#
.SYNOPSIS
  Bring up moharrik and publish it at https://dev.moharrik.com.

.DESCRIPTION
  The chain, in order:

      Go API            127.0.0.1:8122      (bin/api.exe, .env loaded)
      Next dev server   127.0.0.1:3122      (web/, API_ORIGIN -> the API)
      SSH reverse tunnel 10.10.10.1:4590 -> 127.0.0.1:3122
      Nginx Proxy Manager host 88 -> Cloudflare

  Everything below the tunnel (NPM, DNS) is already configured and persists.
  The three local pieces do not — a workstation reboot, or a reboot of the
  server the tunnel lands on, takes the preview down and nothing notices. That
  is what this is for: the sibling fadymondy.com-v2 checkout has had a dev.ps1
  for a while and this one did not, and it showed the day the server restarted.

  Idempotent: anything already listening or already tunnelled is left alone.

  Note: local :8120 belongs to Mahaam, not to this project. The ports here are
  deliberately 8122/3122/4590 and none of them are shared.

.PARAMETER Restart
  Stop this project's API, web and tunnel first. Use after changing Go code,
  since the API is a compiled binary and a rebuild alone changes nothing.

.EXAMPLE
  .\.setup\dev.ps1
  .\.setup\dev.ps1 -Restart
#>
param(
    [switch]$Restart
)

$ErrorActionPreference = 'Stop'

$Root       = Split-Path -Parent $PSScriptRoot
$ApiPort    = 8122
$WebPort    = 3122
$TunnelPort = 4590
$TunnelHost = 'root@100.70.229.51'
$TunnelKey  = "$env:USERPROFILE\.ssh\cabrain_tunnel"
$PublicUrl  = 'https://dev.moharrik.com'

function Test-Port([int]$Port) {
    # A TCP connect rather than Get-NetTCPConnection: what matters is whether
    # something answers, not whether a socket is in some listening state.
    try {
        $c = [System.Net.Sockets.TcpClient]::new()
        $ok = $c.ConnectAsync('127.0.0.1', $Port).Wait(1500)
        $c.Close()
        return $ok
    } catch { return $false }
}

function Get-ProjectProcesses {
    # Matched on the command line, so the sibling checkouts' processes — which
    # run the same binaries on other ports — are never touched.
    $procs = @{}

    $procs.Api = Get-CimInstance Win32_Process -Filter "Name='api.exe'" |
        Where-Object { $_.CommandLine -like '*moharrik*' }

    $procs.Web = Get-CimInstance Win32_Process -Filter "Name='node.exe'" |
        Where-Object { $_.CommandLine -like '*moharrik\web*' -and $_.CommandLine -like '*next*' }

    $procs.Tunnel = Get-CimInstance Win32_Process -Filter "Name='ssh.exe'" |
        Where-Object { $_.CommandLine -like "*10.10.10.1:${TunnelPort}:*" }

    return $procs
}

if ($Restart) {
    Write-Host 'Stopping this project''s processes...' -ForegroundColor Yellow
    $existing = Get-ProjectProcesses
    foreach ($set in @($existing.Api, $existing.Web, $existing.Tunnel)) {
        foreach ($p in $set) {
            Stop-Process -Id $p.ProcessId -Force -ErrorAction SilentlyContinue
            Write-Host "  stopped pid $($p.ProcessId)"
        }
    }
    Start-Sleep -Seconds 2
}

# --- 1. the Go API -------------------------------------------------------
if (Test-Port $ApiPort) {
    Write-Host "API already answering on :$ApiPort" -ForegroundColor DarkGray
} else {
    Write-Host "Starting the API on :$ApiPort..." -ForegroundColor Cyan

    Write-Host '  building bin/api.exe'
    Push-Location $Root
    & go build -o bin/api.exe ./cmd/api
    Pop-Location
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }

    # .env is loaded into this process so the child inherits it.
    Get-Content "$Root\.env" | ForEach-Object {
        if ($_ -match '^\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$') {
            $value = $matches[2].Trim()
            # Strip an inline comment, but only when it is not inside a value
            # that legitimately contains '#' (a password, a colour).
            if ($value -match '^\s*([^"#]*?)\s+#') { $value = $matches[1].Trim() }
            [Environment]::SetEnvironmentVariable($matches[1], $value.Trim('"'), 'Process')
        }
    }
    $env:ADDR    = ":$ApiPort"
    $env:APP_URL = $PublicUrl

    Start-Process -FilePath "$Root\bin\api.exe" -WorkingDirectory $Root `
        -RedirectStandardOutput "$Root\api.dev.log" -RedirectStandardError "$Root\api.err.log" `
        -WindowStyle Hidden | Out-Null

    for ($i = 0; $i -lt 20 -and -not (Test-Port $ApiPort); $i++) { Start-Sleep -Seconds 1 }
    if (Test-Port $ApiPort) { Write-Host "  up on :$ApiPort" -ForegroundColor Green }
    else { throw "API did not come up — see $Root\api.err.log" }
}

# The API has what it needs; the browser must not inherit it. Next inlines
# every NEXT_PUBLIC_* variable into the client bundle, and an absolute
# localhost URL there is fine on this machine and ERR_CONNECTION_REFUSED from
# anywhere else. The app wants same-origin /api.
Get-ChildItem Env: | Where-Object { $_.Name -like 'NEXT_PUBLIC_*' } | ForEach-Object {
    [Environment]::SetEnvironmentVariable($_.Name, $null, 'Process')
}

# --- 2. the Next dev server ---------------------------------------------
if (Test-Port $WebPort) {
    Write-Host "Web already answering on :$WebPort" -ForegroundColor DarkGray
} else {
    Write-Host "Starting the web dev server on :$WebPort..." -ForegroundColor Cyan

    $env:API_ORIGIN = "http://localhost:$ApiPort"
    Start-Process -FilePath 'cmd.exe' `
        -ArgumentList '/c', "npx next dev -p $WebPort -H 127.0.0.1 > `"$Root\web.dev.log`" 2>&1" `
        -WorkingDirectory "$Root\web" -WindowStyle Hidden | Out-Null

    # Turbopack's first compile is slow from cold; 90s is generous rather than
    # optimistic, because failing here sends someone debugging a working setup.
    for ($i = 0; $i -lt 90 -and -not (Test-Port $WebPort); $i++) { Start-Sleep -Seconds 1 }
    if (Test-Port $WebPort) { Write-Host "  up on :$WebPort" -ForegroundColor Green }
    else { throw "Web did not come up — see $Root\web.dev.log" }
}

# --- 3. the tunnel -------------------------------------------------------
$tunnel = (Get-ProjectProcesses).Tunnel
if ($tunnel) {
    Write-Host "Tunnel already up (pid $($tunnel.ProcessId -join ', '))" -ForegroundColor DarkGray
} else {
    Write-Host "Opening the reverse tunnel 10.10.10.1:$TunnelPort -> 127.0.0.1:$WebPort..." -ForegroundColor Cyan

    # ExitOnForwardFailure matters: without it ssh stays connected after the
    # remote bind fails, so the tunnel looks up while forwarding nothing. It
    # also means a stale bind left behind by a dropped session makes this fail
    # loudly — wait for sshd to reap it rather than binding a second port.
    $sshArgs = @(
        '-N', '-i', $TunnelKey,
        '-o', 'BatchMode=yes',
        '-o', 'ExitOnForwardFailure=yes',
        '-o', 'ServerAliveInterval=30',
        '-o', 'ServerAliveCountMax=3',
        '-o', 'StrictHostKeyChecking=accept-new',
        '-R', "10.10.10.1:${TunnelPort}:127.0.0.1:${WebPort}",
        $TunnelHost
    )

    $ssh = Start-Process -FilePath 'C:\Windows\System32\OpenSSH\ssh.exe' -ArgumentList $sshArgs `
        -PassThru -WindowStyle Hidden
    Start-Sleep -Seconds 5

    if ($ssh.HasExited) { throw 'The tunnel exited immediately — check the key and the remote bind.' }
    Write-Host "  tunnel pid $($ssh.Id)" -ForegroundColor Green
}

# --- 4. prove it end to end ---------------------------------------------
Write-Host "`nChecking $PublicUrl ..." -ForegroundColor Cyan

# A freshly opened tunnel can take a few seconds to be usable at the edge;
# retrying here avoids reporting a working chain as broken.
$status = $null
for ($i = 0; $i -lt 6; $i++) {
    try {
        $status = (Invoke-WebRequest -Uri "$PublicUrl/en" -UseBasicParsing -TimeoutSec 20).StatusCode
        if ($status -eq 200) { break }
    } catch { Start-Sleep -Seconds 5 }
}

if ($status -eq 200) {
    Write-Host "`n  $PublicUrl -> 200" -ForegroundColor Green
    Write-Host "  API   http://127.0.0.1:$ApiPort"
    Write-Host "  Web   http://127.0.0.1:$WebPort"
} else {
    Write-Host "`n  $PublicUrl is not answering yet." -ForegroundColor Yellow
    Write-Host '  The local pieces are up; this is usually edge propagation. Retry in a minute.'
}
