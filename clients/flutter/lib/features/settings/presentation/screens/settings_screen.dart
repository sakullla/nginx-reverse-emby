import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../../core/design/theme/accent_themes.dart';
import '../../../../core/design/theme/theme_controller.dart';
import '../../../auth/presentation/providers/auth_provider.dart';

class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final themeAsync = ref.watch(themeControllerProvider);

    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: themeAsync.when(
        data: (settings) => ListView(
          children: [
            _SectionHeader(title: 'Appearance', icon: Icons.palette),
            ListTile(
              leading: const Icon(Icons.color_lens),
              title: const Text('Accent Color'),
              trailing: Wrap(
                spacing: 8,
                children: AccentThemes.all.map((accent) => InkWell(
                  onTap: () => ref
                      .read(themeControllerProvider.notifier)
                      .setAccent(accent.name),
                  child: Container(
                    width: 28,
                    height: 28,
                    decoration: BoxDecoration(
                      gradient: accent.primaryGradient,
                      shape: BoxShape.circle,
                      border: settings.accent.name == accent.name
                          ? Border.all(color: Colors.white, width: 2)
                          : null,
                    ),
                  ),
                )).toList(),
              ),
            ),
            const Divider(),
            _SectionHeader(title: 'Connection', icon: Icons.link),
            ListTile(
              leading: const Icon(Icons.logout),
              title: const Text('Disconnect', style: TextStyle(color: Colors.red)),
              onTap: () => ref.read(authNotifierProvider.notifier).logout(),
            ),
            const Divider(),
            _SectionHeader(title: 'About', icon: Icons.info),
            const ListTile(
              leading: Icon(Icons.app_shortcut),
              title: Text('Application'),
              subtitle: Text('NRE Client'),
            ),
            const ListTile(
              leading: Icon(Icons.tag),
              title: Text('Version'),
              subtitle: Text('2.1.0'),
            ),
          ],
        ),
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (_, __) => const Center(child: Text('Error')),
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.title, required this.icon});
  final String title;
  final IconData icon;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
      child: Row(
        children: [
          Icon(icon, size: 18, color: scheme.primary),
          const SizedBox(width: 8),
          Text(
            title,
            style: TextStyle(
              color: scheme.primary,
              fontWeight: FontWeight.bold,
            ),
          ),
        ],
      ),
    );
  }
}
