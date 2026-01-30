import 'dart:convert';
import 'package:flutter/cupertino.dart';
import 'package:web_socket_channel/web_socket_channel.dart';
import 'package:web_socket_channel/io.dart';
import '../models/devices.dart';

class BackendService {
  WebSocketChannel? _channel;
  bool get isConnected => _channel != null;

  Function(Map<String, dynamic>)? onMessage;

  Future<void> connect(String url) async {
    if (_channel != null) return;

    _channel = WebSocketChannel.connect(Uri.parse(url));

    _channel!.stream.listen(
          (data) {
        final msg = jsonDecode(data);
        onMessage?.call(msg);
      },
      onDone: () {
        _channel = null;
      },
      onError: (e) {
        _channel = null;
      },
    );
  }

  void disconnect() {
    _channel?.sink.close();
    _channel = null;
  }

  void send(String type, Map<String, dynamic> data) {
    if (_channel == null) {
      debugPrint('WebSocket not connected');
      return;
    }

    final payload = {'type': type, 'data': data};
    _channel!.sink.add(jsonEncode(payload));
  }

  void becomeHost() => send('become_host', {});
  void scanDevices() => send('scan_devices', {});
  void connectToDevice(String address) => send('connect_device', {'address': address});
  void selectFile(String path) => send('select_file', {'path': path});
  void play() => send('play', {});
  void pause() => send('pause', {});
  void stop() => send('stop', {});
  void setVolume(double level) => send('volume', {'level': level});
}