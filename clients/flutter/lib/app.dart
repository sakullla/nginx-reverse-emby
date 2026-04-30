import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'core/routing/app_router.dart';
import 'core/theme/app_theme.dart';
import 'core/theme/theme_controller.dart';

class NreClientApp extends ConsumerWidget {
  const NreClientApp({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final router = ref.watch(routerProvider);
    final themeAsync = ref.watch(themeControllerProvider);

    return themeAsync.when(
      data: (settings) => MaterialApp.router(
        title: 'NRE Client',
        debugShowCheckedModeBanner: false,
        themeMode: settings.themeMode,
        theme: AppTheme.buildTheme(settings.colorScheme, ThemeMode.light),
        darkTheme: AppTheme.buildTheme(settings.colorScheme, ThemeMode.dark),
        routerConfig: router,
        localizationsDelegates: const [],
        supportedLocales: const [Locale('en'), Locale('zh')],
      ),
      loading: () => const MaterialApp(
        home: Scaffold(body: Center(child: CircularProgressIndicator())),
      ),
      error: (_, __) => const MaterialApp(
        home: Scaffold(body: Center(child: Text('Failed to load theme'))),
      ),
    );
  }
}
