# LAN-BT_AUDIO_AMPLIFIER

Audio Sharing Application allowing for multiple people to play the same audio and amplify the audio using their phone or laptop devices without need of any peripheral device.

Uses mDNS and ZeroConf for Network Discovery and to connect to devices, then uses Websockets to send the Audio Stream

# PowerShell Launch Script Usage

**Before running the script be sure to Compile the Go scripts using command**

``` powershell
go build -o LAN_BT.exe .
```

`launch.ps1` script allows you to easily run multiple instances of the LAN-BT Audio Amplifier for testing host/client configurations.

## Usage Options

### Default: Dual Instance Mode (Host + Client)
```powershell
.\launch.ps1
# or
.\launch.ps1 -Mode dual
```
This starts two instances:
- **Host Instance**: Port 9090
- **Client Instance**: Port 9091

### Host Only Mode
```powershell
.\launch.ps1 -Mode host
```
Starts only the host instance on port 9090.

### Client Only Mode  
```powershell
.\launch.ps1 -Mode client
```
Starts only the client instance on port 9091.

### Custom Ports
```powershell
.\launch.ps1 -Mode dual -HostPort 9090 -ClientPort 9091
```

### Build Mode (creates executable files)
```powershell
.\launch.ps1 -Mode dual -Build
```

## Workflow for Testing

### Step 1: Start Both Instances
```powershell
.\launch.ps1
```

### Step 2: Configure the Host
1. In the **Host** window (port 9090):
   - Click "Be Host"
   - Select an audio file using "📁 Select Audio File"
   - Click "▶ Play" to start streaming

### Step 3: Connect the Client
1. In the **Client** window (port 9091):
   - Click "Scan Devices" 
   - The host should appear in the device list
   - Wait until mDNS discovery ends
   - Select the host and click "Connect"
   - Audio will start playing on the client

### Step 4: Test Features (Audio Playback currently not working)
- **Volume Control**: Adjust volume on either instance
- **Playback Control**: Play/Pause/Stop from the host
- **Progress Tracking**: See real-time progress in both UIs
- **Test Packets**: Send test packets to verify connectivity

## Troubleshooting

### Port Already in Use
If you get port conflicts, specify different ports:
```powershell
.\launch.ps1 -HostPort 9092 -ClientPort 9093
```

### mDNS Discovery Issues
- Check Windows Firewall settings
- Ensure both instances are on the same network
- Try manual connection using IP addresses

### Audio Issues
- Only one instance can play audio locally at a time
- Use headphones to avoid conflicts
- Check system audio device permissions

### Script Permissions
If PowerShell blocks execution:
```powershell
Set-ExecutionPolicy -ExecutionPolicy Bypass -Scope Process
.\launch.ps1
```

## Advanced Usage

### Multiple Hosts and Clients
You can run multiple pairs by using different ports:
```powershell
# Terminal 1
.\launch.ps1 -HostPort 9090 -ClientPort 9091

# Terminal 2  
.\launch.ps1 -HostPort 9092 -ClientPort 9093
```

### Manual Instance Management
For more control, start instances manually:
```powershell
# Host
go run main.go --ui --port=9090
python main.py --port=9090

# Client (in separate terminal)
go run main.go --ui --port=9091  
python main.py --port=9091
```

## Cleanup

Press `Ctrl+C` in the PowerShell window to stop all instances gracefully. The script automatically cleans up all processes.
