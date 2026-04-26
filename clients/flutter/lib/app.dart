import 'package:flutter/material.dart';

import 'screens/about_screen.dart';
import 'screens/logs_screen.dart';
import 'screens/overview_screen.dart';
import 'screens/register_screen.dart';
import 'screens/runtime_screen.dart';
import 'screens/settings_screen.dart';
import 'screens/updates_screen.dart';

class NreClientApp extends StatelessWidget {
  const NreClientApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'NRE Client',
      theme: ThemeData(useMaterial3: true, colorSchemeSeed: Colors.teal),
      home: const NreClientHome(),
    );
  }
}

class NreClientHome extends StatefulWidget {
  const NreClientHome({super.key});

  @override
  State<NreClientHome> createState() => _NreClientHomeState();
}

class _NreClientHomeState extends State<NreClientHome> {
  int index = 0;

  static const screens = [
    OverviewScreen(),
    RegisterScreen(),
    RuntimeScreen(),
    LogsScreen(),
    UpdatesScreen(),
    SettingsScreen(),
    AboutScreen(),
  ];

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: IndexedStack(index: index, children: screens),
      bottomNavigationBar: NavigationBar(
        selectedIndex: index,
        onDestinationSelected: (value) => setState(() => index = value),
        destinations: const [
          NavigationDestination(icon: Icon(Icons.dashboard), label: 'Overview'),
          NavigationDestination(icon: Icon(Icons.login), label: 'Register'),
          NavigationDestination(icon: Icon(Icons.memory), label: 'Runtime'),
          NavigationDestination(icon: Icon(Icons.article), label: 'Logs'),
          NavigationDestination(
            icon: Icon(Icons.system_update),
            label: 'Updates',
          ),
          NavigationDestination(icon: Icon(Icons.settings), label: 'Settings'),
          NavigationDestination(icon: Icon(Icons.info), label: 'About'),
        ],
      ),
    );
  }
}
