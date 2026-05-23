param(
    [string]$Mode = "dual",     # dual | host | client
    [int]$HostPort   = 9090,
    [int]$ClientPort = 9091,
    [switch]$Build
)

$ErrorActionPreference = "Stop"

function Wait-Port {
    param([int]$Port, [int]$TimeoutSec = 20)
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    while ($sw.Elapsed.TotalSeconds -lt $TimeoutSec) {
        try {
            $client = New-Object System.Net.Sockets.TcpClient
            $async  = $client.BeginConnect("127.0.0.1", $Port, $null, $null)
            $ready  = $async.AsyncWaitHandle.WaitOne(500)
            if ($ready -and $client.Connected) {
                $client.Close()
                return $true
            }
            $client.Close()
        } catch {}
        Start-Sleep -Milliseconds 200   # was missing — previously busy-spun at 100% CPU
    }
    return $false
}

function Start-Instance {
    param(
        [int]$Port,
        [string]$Name,
        [string]$Arguments = ""
    )

    Write-Host "Starting $Name on port $Port..." -ForegroundColor Yellow

    if ($Build) {
        & go build -o bin/LAN_BT.exe main.go ui.go
        $backendProc = Start-Process -FilePath (Resolve-Path "bin/LAN_BT.exe") `
                                     -ArgumentList $Arguments -PassThru
    } elseif (Test-Path "LAN_BT.exe") {
        $backendProc = Start-Process -FilePath (Resolve-Path "LAN_BT.exe") `
                                     -ArgumentList $Arguments -PassThru
    } else {
        $goArgs = @("run", "main.go", "ui.go")
        if ($Arguments) { $goArgs += $Arguments.Split(" ") }
        Write-Host "  go $($goArgs -join ' ')" -ForegroundColor Gray
        $backendProc = Start-Process -FilePath "go" -ArgumentList $goArgs -PassThru
    }

    Start-Sleep -Seconds 3

    if (-not (Wait-Port -Port $Port -TimeoutSec 30)) {
        Write-Host "$Name did not start on port $Port." -ForegroundColor Red
        if (-not $backendProc.HasExited) { $backendProc.Kill() }
        exit 1
    }

    $uiProc = Start-Process -FilePath "pythonw" `
                             -ArgumentList @("main.py", "--port", $Port) -PassThru

    Write-Host "$Name ready  [backend PID $($backendProc.Id)  UI PID $($uiProc.Id)]" `
               -ForegroundColor Green

    return @{ Backend = $backendProc; UI = $uiProc; Port = $Port; Name = $Name }
}

# ── Main ───────────────────────────────────────────────────────────────────

Write-Host "LAN-BT Audio Amplifier" -ForegroundColor Cyan
Write-Host "======================" -ForegroundColor Cyan

switch ($Mode) {
    "dual" {
        Write-Host "Starting host + client instances…" -ForegroundColor Yellow
        $hostInst   = Start-Instance -Port $HostPort   -Name "Host"   -Arguments "--port=$HostPort"
        Start-Sleep -Seconds 2
        $clientInst = Start-Instance -Port $ClientPort -Name "Client" -Arguments "--port=$ClientPort"

        Write-Host ""
        Write-Host "Host:    http://localhost:$HostPort"   -ForegroundColor White
        Write-Host "Client:  http://localhost:$ClientPort" -ForegroundColor White
        Write-Host ""
        Write-Host "Host:   click 'Be Host' -> select file -> Play" -ForegroundColor Cyan
        Write-Host "Client: click 'Scan Devices' -> select host -> Connect" -ForegroundColor Cyan

        $processes = @($hostInst.Backend, $hostInst.UI, $clientInst.Backend, $clientInst.UI)
    }

    "host" {
        $hostInst  = Start-Instance -Port $HostPort -Name "Host" -Arguments "--port=$HostPort"
        $processes = @($hostInst.Backend, $hostInst.UI)
    }

    "client" {
        $clientInst = Start-Instance -Port $ClientPort -Name "Client" -Arguments "--port=$ClientPort"
        $processes  = @($clientInst.Backend, $clientInst.UI)
    }

    default {
        Write-Host "Invalid mode. Use: -Mode dual | host | client" -ForegroundColor Red
        exit 1
    }
}

# ── Cleanup on Ctrl+C ──────────────────────────────────────────────────────

$cleanupScript = {
    Write-Host "`nStopping all instances…" -ForegroundColor Yellow
    foreach ($proc in $using:processes) {
        if (-not $proc.HasExited) {
            Write-Host "  Stopping PID $($proc.Id)" -ForegroundColor Gray
            $proc.Kill()
        }
    }
    Write-Host "Done." -ForegroundColor Green
}

try {
    Write-Host ""
    Write-Host "Press Ctrl+C to stop." -ForegroundColor Cyan
    while ($true) { Start-Sleep -Seconds 1 }
} finally {
    & $cleanupScript
}
