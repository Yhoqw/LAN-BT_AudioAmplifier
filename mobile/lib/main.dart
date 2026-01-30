import 'package:flutter/material.dart';
import 'ui/home_page.dart';
import 'services/backend.dart';

void main() {
  runApp(MyApp());
}

class MyApp extends StatelessWidget {
  final backend = BackendService();

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Audio Amplifier',
      theme: ThemeData(primarySwatch: Colors.blue),
      home: HomePage(backend: backend),
    );
  }
}
