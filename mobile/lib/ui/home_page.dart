import 'package:flutter/material.dart';
import '../models/devices.dart';
import '../services/backend.dart';

class HomePage extends StatefulWidget {
  final BackendService backend;
  const HomePage({required this.backend, super.key});

  @override
  State<HomePage> createState() => _HomePageState();
}

class _HomePageState extends State<HomePage> {
  List<Device> devices = [];
  List<String> connectedDevices = [];
  bool isHost = false;
  bool isStreaming = false;
  double volume = 70;
  String currentTrack = "";

  @override
  void initState() {
    super.initState();
    widget.backend.onMessage = handleBackendMessage;
    widget.backend.connect('ws://192.168.0.100:9090/ws'); // Your LAN IP
  }

  @override
  void dispose() {
    widget.backend.disconnect();
    super.dispose();
  }

  void handleBackendMessage(Map<String, dynamic> msg) {
    final type = msg['type'] ?? msg['Type'];
    final data = msg['data'] ?? msg['Data'] ?? {};

    switch (type) {
      case 'host_started':
        setState(() => isHost = true);
        break;
      case 'device_found':
        final device = Device(
          name: data['name'] ?? '',
          type: data['type'] ?? '',
          status: 'Available',
          address: data['address'] ?? '',
        );
        setState(() => devices.add(device));
        break;
      case 'connected':
        setState(() => connectedDevices.add(data['name'] ?? 'Remote Host'));
        break;
      case 'playback_started':
        setState(() => isStreaming = true);
        break;
      case 'playback_paused':
      case 'playback_stopped':
        setState(() => isStreaming = false);
        break;
      case 'volume_changed':
        setState(() => volume = (data['level'] ?? 70).toDouble());
        break;
      case 'file_loaded':
        setState(() => currentTrack = data['filename'] ?? '');
        break;
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text('Audio Amplifier')),
      body: SingleChildScrollView(
        padding: EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                ElevatedButton(onPressed: widget.backend.becomeHost, child: Text('Be Host')),
                SizedBox(width: 10),
                ElevatedButton(onPressed: widget.backend.scanDevices, child: Text('Scan Devices')),
              ],
            ),
            SizedBox(height: 10),
            Text('Available Devices:', style: TextStyle(fontWeight: FontWeight.bold)),
            SizedBox(
              height: 150,
              child: ListView.builder(
                itemCount: devices.length,
                itemBuilder: (_, i) => ListTile(
                  title: Text(devices[i].name),
                  subtitle: Text('${devices[i].type} - ${devices[i].status}'),
                  trailing: IconButton(
                    icon: Icon(Icons.link),
                    onPressed: () => widget.backend.connectToDevice(devices[i].address),
                  ),
                ),
              ),
            ),
            SizedBox(height: 10),
            Text('Connected Devices:', style: TextStyle(fontWeight: FontWeight.bold)),
            Column(
              children: connectedDevices.map((d) => Text(d)).toList(),
            ),
            SizedBox(height: 10),
            Text('Now Playing: $currentTrack'),
            Slider(
              value: volume,
              min: 0,
              max: 100,
              onChanged: (v) {
                setState(() => volume = v);
                widget.backend.setVolume(v);
              },
            ),
            Row(
              children: [
                ElevatedButton(
                    onPressed: isStreaming ? widget.backend.pause : widget.backend.play,
                    child: Text(isStreaming ? '⏸ Pause' : '▶ Play')),
                SizedBox(width: 10),
                ElevatedButton(onPressed: widget.backend.stop, child: Text('⏹ Stop')),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
