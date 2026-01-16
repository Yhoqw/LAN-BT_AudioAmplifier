param(
    [string]$BackendMode = "run"
)
$ErrorActionPreference = "Stop"
function Wait-Port {
    param([int]$Port,[int]$TimeoutSec=20)
    $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    while ($stopwatch.Elapsed.TotalSeconds -lt $TimeoutSec) {
        try {
            $client = New-Object System.Net.Sockets.TcpClient
            $async = $client.BeginConnect("127.0.0.1",$Port,$null,$null)
            $wait = $async.AsyncWaitHandle.WaitOne(500)
            if ($wait -and $client.Connected) {
                $client.Close()
                return $true
            }
            $client.Close()
        } catch {}
    }
    return $false
}
if ($BackendMode -eq "build") {
    & go build -o bin/backend.exe main.go
    $backendProc = Start-Process -FilePath (Resolve-Path "bin/backend.exe") -PassThru
} else {
    $backendProc = Start-Process -FilePath "go" -ArgumentList @("run","main.go") -PassThru
}
if (-not (Wait-Port -Port 9090 -TimeoutSec 30)) {
    Write-Host "Backend did not start on port 9090" ; exit 1
}
$uiProc = Start-Process -FilePath "python" -ArgumentList @("main.py") -PassThru
Write-Host "Backend PID: $($backendProc.Id)  UI PID: $($uiProc.Id)"

# This is just to run on my device
# powershell -ExecutionPolicy Bypass -File .\launch.ps1