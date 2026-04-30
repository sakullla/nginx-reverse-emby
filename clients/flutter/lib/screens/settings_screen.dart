import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:path_provider/path_provider.dart';

import '../core/client_state.dart';
import '../l10n/app_localizations.dart';

class SettingsScreen extends StatefulWidget {
  const SettingsScreen({
    super.key,
    required this.state,
    this.onClearProfile,
    this.themeMode = ThemeMode.system,
    this.onThemeModeChanged,
  });

  final ClientState state;
  final VoidCallback? onClearProfile;
  final ThemeMode themeMode;
  final ValueChanged<ThemeMode>? onThemeModeChanged;

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  String _dataDir = '';
  final String _appVersion = '2.0.0';

  @override
  void initState() {
    super.initState();
    _loadPaths();
  }

  Future<void> _loadPaths() async {
    try {
      final dir = await getApplicationSupportDirectory();
      if (mounted) setState(() => _dataDir = dir.path);
    } catch (_) {
      // ignore
    }
  }

  Future<void> _exportConfig() async {
    final l10n = AppLocalizations.of(context)!;
    final profile = widget.state.profile;
    if (!profile.isRegistered) {
      _showSnack(l10n.msgNoProfileToExport);
      return;
    }
    final json = profile.toJson().toString();
    await Clipboard.setData(ClipboardData(text: json));
    _showSnack(l10n.msgProfileExported);
  }

  void _showSnack(String message) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message), duration: const Duration(seconds: 2)),
    );
  }

  Future<void> _showClearDataDialog() async {
    final l10n = AppLocalizations.of(context)!;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(l10n.titleClearAllData),
        content: Text(l10n.descClearAllData),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: Text(l10n.btnCancel),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(context).colorScheme.error,
            ),
            child: Text(l10n.btnClear),
          ),
        ],
      ),
    );
    if (confirmed == true) {
      widget.onClearProfile?.call();
      _showSnack(l10n.msgAllDataCleared);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l10n = AppLocalizations.of(context)!;
    final profile = widget.state.profile;
    final isRegistered = profile.isRegistered;

    return Scaffold(
      appBar: AppBar(title: Text(l10n.titleSettings)),
      body: ListView(
        children: [
          _SectionHeader(title: l10n.titleConnection, icon: Icons.link),
          _SettingTile(
            icon: Icons.dns,
            title: l10n.labelMasterUrl,
            subtitle: profile.masterUrl.isEmpty ? l10n.labelNotConfigured : profile.masterUrl,
            trailing: isRegistered
                ? IconButton(
                    icon: const Icon(Icons.copy, size: 18),
                    onPressed: () {
                      Clipboard.setData(ClipboardData(text: profile.masterUrl));
                      _showSnack(l10n.msgCopied);
                    },
                  )
                : null,
          ),
          _SettingTile(
            icon: Icons.badge,
            title: l10n.labelAgentId,
            subtitle: profile.agentId.isEmpty ? l10n.labelNotRegistered : profile.agentId,
            trailing: isRegistered
                ? IconButton(
                    icon: const Icon(Icons.copy, size: 18),
                    onPressed: () {
                      Clipboard.setData(ClipboardData(text: profile.agentId));
                      _showSnack(l10n.msgCopied);
                    },
                  )
                : null,
          ),
          _SettingTile(
            icon: Icons.key,
            title: l10n.labelToken,
            subtitle: isRegistered ? '••••••••${profile.token.length > 8 ? profile.token.substring(profile.token.length - 4) : ''}' : l10n.labelNotRegistered,
          ),

          const Divider(),

          _SectionHeader(title: l10n.titleLocalStorage, icon: Icons.storage),
          _SettingTile(
            icon: Icons.folder,
            title: l10n.labelDataDir,
            subtitle: _dataDir.isEmpty ? l10n.valueLoading : _dataDir,
            trailing: _dataDir.isNotEmpty
                ? IconButton(
                    icon: const Icon(Icons.copy, size: 18),
                    onPressed: () {
                      Clipboard.setData(ClipboardData(text: _dataDir));
                      _showSnack(l10n.msgCopied);
                    },
                  )
                : null,
          ),
          ListTile(
            leading: const Icon(Icons.upload_file),
            title: Text(l10n.titleExportProfile),
            subtitle: Text(l10n.descExportProfile),
            onTap: _exportConfig,
          ),
          ListTile(
            leading: Icon(Icons.delete_forever, color: theme.colorScheme.error),
            title: Text(l10n.titleClearAllData, style: TextStyle(color: theme.colorScheme.error)),
            subtitle: Text(l10n.descClearData),
            onTap: _showClearDataDialog,
          ),

          const Divider(),

          _SectionHeader(title: l10n.titleSystem, icon: Icons.computer),
          ListTile(
            leading: const Icon(Icons.login),
            title: Text(l10n.titleStartAtLogin),
            subtitle: Text(l10n.descStartAtLogin),
            trailing: const _StartAtLoginSwitch(),
          ),
          _SettingTile(
            icon: Icons.info,
            title: l10n.labelPlatform,
            subtitle: Platform.operatingSystem,
          ),
          _ThemeSelector(
            themeMode: widget.themeMode,
            onChanged: widget.onThemeModeChanged,
          ),

          const Divider(),

          _SectionHeader(title: l10n.titleAbout, icon: Icons.info_outline),
          _SettingTile(
            icon: Icons.app_shortcut,
            title: l10n.labelApplication,
            subtitle: l10n.valueAppName,
          ),
          _SettingTile(
            icon: Icons.tag,
            title: l10n.labelVersion,
            subtitle: _appVersion,
          ),
          _SettingTile(
            icon: Icons.code,
            title: l10n.labelDistribution,
            subtitle: l10n.valueGithubRelease,
          ),
          _SettingTile(
            icon: Icons.cloud_off,
            title: l10n.labelContainerPolicy,
            subtitle: l10n.valueContainerPolicyDesc,
          ),
        ],
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
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
      child: Row(
        children: [
          Icon(icon, size: 18, color: theme.colorScheme.primary),
          const SizedBox(width: 8),
          Text(
            title,
            style: theme.textTheme.titleSmall?.copyWith(
              color: theme.colorScheme.primary,
              fontWeight: FontWeight.bold,
            ),
          ),
        ],
      ),
    );
  }
}

class _SettingTile extends StatelessWidget {
  const _SettingTile({
    required this.icon,
    required this.title,
    required this.subtitle,
    this.trailing,
  });

  final IconData icon;
  final String title;
  final String subtitle;
  final Widget? trailing;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      leading: Icon(icon),
      title: Text(title),
      subtitle: Text(
        subtitle,
        maxLines: 2,
        overflow: TextOverflow.ellipsis,
      ),
      trailing: trailing,
    );
  }
}

class _StartAtLoginSwitch extends StatefulWidget {
  const _StartAtLoginSwitch();

  @override
  State<_StartAtLoginSwitch> createState() => _StartAtLoginSwitchState();
}

class _StartAtLoginSwitchState extends State<_StartAtLoginSwitch> {
  bool _value = false;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;
    return Switch(
      value: _value,
      onChanged: (value) {
        setState(() => _value = value);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(value
                ? l10n.msgStartAtLoginEnabled
                : l10n.msgStartAtLoginDisabled),
          ),
        );
      },
    );
  }
}

class _ThemeSelector extends StatelessWidget {
  const _ThemeSelector({
    required this.themeMode,
    this.onChanged,
  });

  final ThemeMode themeMode;
  final ValueChanged<ThemeMode>? onChanged;

  @override
  Widget build(BuildContext context) {
    final l10n = AppLocalizations.of(context)!;

    return ListTile(
      leading: const Icon(Icons.dark_mode),
      title: Text(l10n.labelTheme),
      trailing: ConstrainedBox(
        constraints: const BoxConstraints(minWidth: 120),
        child: DropdownButton<ThemeMode>(
          value: themeMode,
          isDense: true,
          underline: const SizedBox.shrink(),
          alignment: AlignmentDirectional.centerEnd,
          items: [
            DropdownMenuItem(
              value: ThemeMode.system,
              child: Text(l10n.valueThemeSystem),
            ),
            DropdownMenuItem(
              value: ThemeMode.light,
              child: Text(l10n.valueThemeLight),
            ),
            DropdownMenuItem(
              value: ThemeMode.dark,
              child: Text(l10n.valueThemeDark),
            ),
          ],
          onChanged: onChanged == null
              ? null
              : (mode) {
                  if (mode != null) onChanged!(mode);
                },
        ),
      ),
    );
  }
}
