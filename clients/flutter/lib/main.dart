import 'dart:io';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:window_manager/window_manager.dart';
import 'package:tray_manager/tray_manager.dart' as tray;
import 'app.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();

  if (_isDesktop) {
    await windowManager.ensureInitialized();
    await windowManager.waitUntilReadyToShow(
      const WindowOptions(
        size: Size(1024, 700),
        minimumSize: Size(720, 520),
        center: true,
        title: 'NRE Client',
        backgroundColor: Colors.transparent,
        skipTaskbar: false,
      ),
      () async {
        await windowManager.show();
        await windowManager.focus();
      },
    );

    await _setupTray();
    windowManager.addListener(_WindowCloseHandler());
  }

  runApp(const ProviderScope(child: NreClientApp()));
}

bool get _isDesktop =>
    Platform.isWindows || Platform.isMacOS || Platform.isLinux;

Future<void> _setupTray() async {
  try {
    await tray.trayManager.setToolTip('NRE Client');
    await tray.trayManager.setContextMenu(
      tray.Menu(
        items: [
          tray.MenuItem(key: 'show', label: 'Show'),
          tray.MenuItem.separator(),
          tray.MenuItem(key: 'quit', label: 'Quit'),
        ],
      ),
    );
    tray.trayManager.addListener(_TrayHandler());
  } catch (_) {
    // Tray may not be available
  }
}

class _TrayHandler extends tray.TrayListener {
  @override
  void onTrayIconMouseDown() => tray.trayManager.popUpContextMenu();

  @override
  void onTrayMenuItemClick(tray.MenuItem menuItem) async {
    switch (menuItem.key) {
      case 'show':
        await windowManager.show();
        await windowManager.focus();
      case 'quit':
        await tray.trayManager.destroy();
        await windowManager.close();
    }
  }
}

class _WindowCloseHandler extends WindowListener {
  @override
  void onWindowClose() async => await windowManager.hide();
}
