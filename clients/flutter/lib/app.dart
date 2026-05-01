import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'core/design/theme/theme_controller.dart';
import 'core/design/tokens/app_colors.dart';
import 'core/routing/app_router.dart';

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
        theme: settings.themeData,
        routerConfig: router,
        localizationsDelegates: const [
          GlobalMaterialLocalizations.delegate,
          GlobalWidgetsLocalizations.delegate,
          GlobalCupertinoLocalizations.delegate,
        ],
        supportedLocales: const [Locale('en'), Locale('zh')],
        builder: (context, child) {
          return Container(
            decoration: const BoxDecoration(
              gradient: AppColors.backgroundGradient,
            ),
            child: child,
          );
        },
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
