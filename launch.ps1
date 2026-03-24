param(
    [string]$Mode = "dual",  # Options: "dual", "host", "client"
    [int]$HostPort = 9090,
    [int]$ClientPort = 9091,
    [switch]$Build
)

$ErrorActionPreference = "Stop"

function Wait-Port {
    param([int]$Port, [int]$TimeoutSec = 20)
    $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    while ($stopwatch.Elapsed.TotalSeconds -lt $TimeoutSec) {
        try {
            $client = New-Object System.Net.Sockets.TcpClient
            $async = $client.BeginConnect("127.0.0.1", $Port, $null, $null)
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

function Start-Instance {
    param(
        [int]$Port,
        [string]$Name,
        [string]$Arguments = ""
    )
    
    Write-Host "Starting $Name on port $Port..."
    
    if ($Build) {
        & go build -o bin/LAN_BT.exe main.go ui.go
        $backendProc = Start-Process -FilePath (Resolve-Path "bin/LAN_BT.exe") -ArgumentList $Arguments -PassThru
    } elseif (Test-Path "LAN_BT.exe") {
        # Use pre-built executable if it exists
        $backendProc = Start-Process -FilePath (Resolve-Path "LAN_BT.exe") -ArgumentList $Arguments -PassThru
    } else {
        # Fall back to go run
        $goArgs = @("run", "main.go", "ui.go")
        if ($Arguments) {
            $goArgs += $Arguments.Split(" ")
        }
        Write-Host "Using: go $($goArgs -join ' ')" -ForegroundColor Gray
        $backendProc = Start-Process -FilePath "go" -ArgumentList $goArgs -PassThru
    }
    
    Write-Host "Waiting for $Name to start on port $Port..." -ForegroundColor Yellow
    Start-Sleep -Seconds 3  # Give more time for initial startup
    
    if (-not (Wait-Port -Port $Port -TimeoutSec 30)) {
        Write-Host "$Name did not start on port $Port" -ForegroundColor Red
        Write-Host "Checking if process is still running..." -ForegroundColor Yellow
        if (-not $backendProc.HasExited) {
            Write-Host "Process is running but port not accessible. Killing process..." -ForegroundColor Red
            $backendProc.Kill()
        }
        exit 1
    }
    
    $uiProc = Start-Process -FilePath "pythonw" -ArgumentList @("main.py", "--port", $Port) -PassThru
    
    Write-Host "$Name Backend PID: $($backendProc.Id)  UI PID: $($uiProc.Id)" -ForegroundColor Green
    return @{
        Backend = $backendProc
        UI = $uiProc
        Port = $Port
        Name = $Name
    }
}

# Main execution
Write-Host "LAN-BT Audio Amplifier Launcher" -ForegroundColor Cyan
Write-Host "================================" -ForegroundColor Cyan

switch ($Mode) {
    "dual" {
        Write-Host "Starting dual instances (Host + Client)..." -ForegroundColor Yellow
        
        # Start Host Instance
        $hostInstance = Start-Instance -Port $HostPort -Name "Host" -Arguments "--ui --port=$HostPort"
        Start-Sleep -Seconds 2  # Give host time to fully initialize
        
        # Start Client Instance  
        $clientInstance = Start-Instance -Port $ClientPort -Name "Client" -Arguments "--ui --port=$ClientPort"
        
        Write-Host ""
        Write-Host "Both instances started!" -ForegroundColor Green
        Write-Host "Host: http://localhost:$HostPort" -ForegroundColor White
        Write-Host "Client: http://localhost:$ClientPort" -ForegroundColor White
        Write-Host ""
        Write-Host "Use the Host instance to:" -ForegroundColor Cyan
        Write-Host "  1. Click 'Be Host' to start hosting" -ForegroundColor Gray
        Write-Host "  2. Select an audio file" -ForegroundColor Gray
        Write-Host "  3. Start playback" -ForegroundColor Gray
        Write-Host ""
        Write-Host "Use the Client instance to:" -ForegroundColor Cyan
        Write-Host "  1. Click 'Scan Devices' to find the host" -ForegroundColor Gray
        Write-Host "  2. Select and connect to the host" -ForegroundColor Gray
        Write-Host "  3. Receive audio stream" -ForegroundColor Gray
        
        # Keep track of processes for cleanup
        $processes = @($hostInstance.Backend, $hostInstance.UI, $clientInstance.Backend, $clientInstance.UI)
    }
    
    "host" {
        Write-Host "Starting Host instance only..." -ForegroundColor Yellow
        $hostInstance = Start-Instance -Port $HostPort -Name "Host" -Arguments "--ui --port=$HostPort"
        Write-Host "Host started: http://localhost:$HostPort" -ForegroundColor Green
        $processes = @($hostInstance.Backend, $hostInstance.UI)
    }
    
    "client" {
        Write-Host "Starting Client instance only..." -ForegroundColor Yellow
        $clientInstance = Start-Instance -Port $ClientPort -Name "Client" -Arguments "--ui --port=$ClientPort"
        Write-Host "Client started: http://localhost:$ClientPort" -ForegroundColor Green
        $processes = @($clientInstance.Backend, $clientInstance.UI)
    }
    
    default {
        Write-Host "Invalid mode. Use: -Mode dual, host, or client" -ForegroundColor Red
        exit 1
    }
}

# Cleanup function
$cleanupScript = {
    Write-Host "`nStopping all instances..." -ForegroundColor Yellow
    foreach ($proc in $using:processes) {
        if (-not $proc.HasExited) {
            Write-Host "Stopping PID $($proc.Id)..." -ForegroundColor Gray
            $proc.Kill()
        }
    }
    Write-Host "All instances stopped." -ForegroundColor Green
}

# Set up cleanup on script exit
$originalErrorActionPreference = $ErrorActionPreference
try {
    # Wait for user input to stop
    Write-Host ""
    Write-Host "Press Ctrl+C to stop all instances..." -ForegroundColor Cyan
    while ($true) {
        Start-Sleep -Seconds 1
    }
} finally {
    & $cleanupScript
    $ErrorActionPreference = $originalErrorActionPreference
}
