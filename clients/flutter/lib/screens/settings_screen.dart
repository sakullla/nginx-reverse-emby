import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:path_provider/path_provider.dart';

import '../core/client_state.dart';

class SettingsScreen extends StatefulWidget {
  const SettingsScreen({
    super.key,
    required this.state,
    this.onClearProfile,
  });

  final ClientState state;
  final VoidCallback? onClearProfile;

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
    final profile = widget.state.profile;
    if (!profile.isRegistered) {
      _showSnack('No registered profile to export');
      return;
    }
    final json = profile.toJson().toString();
    await Clipboard.setData(ClipboardData(text: json));
    _showSnack('Profile JSON copied to clipboard');
  }

  void _showSnack(String message) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(message), duration: const Duration(seconds: 2)),
    );
  }

  Future<void> _showClearDataDialog() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Clear All Data'),
        content: const Text(
          'This will erase all local data including your registration profile. '
          'The agent on the master server will remain but this client will need to be re-registered.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            style: FilledButton.styleFrom(
              backgroundColor: Theme.of(context).colorScheme.error,
            ),
            child: const Text('Clear'),
          ),
        ],
      ),
    );
    if (confirmed == true) {
      widget.onClearProfile?.call();
      _showSnack('All local data cleared');
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final profile = widget.state.profile;
    final isRegistered = profile.isRegistered;

    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        children: [
          // Connection Section
          _SectionHeader(title: 'Connection', icon: Icons.link),
          _SettingTile(
            icon: Icons.dns,
            title: 'Master URL',
            subtitle: profile.masterUrl.isEmpty ? 'Not configured' : profile.masterUrl,
            trailing: isRegistered
                ? IconButton(
                    icon: const Icon(Icons.copy, size: 18),
                    onPressed: () {
                      Clipboard.setData(ClipboardData(text: profile.masterUrl));
                      _showSnack('Copied');
                    },
                  )
                : null,
          ),
          _SettingTile(
            icon: Icons.badge,
            title: 'Agent ID',
            subtitle: profile.agentId.isEmpty ? 'Not registered' : profile.agentId,
            trailing: isRegistered
                ? IconButton(
                    icon: const Icon(Icons.copy, size: 18),
                    onPressed: () {
                      Clipboard.setData(ClipboardData(text: profile.agentId));
                      _showSnack('Copied');
                    },
                  )
                : null,
          ),
          _SettingTile(
            icon: Icons.key,
            title: 'Agent Token',
            subtitle: isRegistered ? '••••••••${profile.token.length > 8 ? profile.token.substring(profile.token.length - 4) : ''}' : 'Not registered',
          ),

          const Divider(),

          // Local Storage Section
          _SectionHeader(title: 'Local Storage', icon: Icons.storage),
          _SettingTile(
            icon: Icons.folder,
            title: 'Data Directory',
            subtitle: _dataDir.isEmpty ? 'Loading...' : _dataDir,
            trailing: _dataDir.isNotEmpty
                ? IconButton(
                    icon: const Icon(Icons.copy, size: 18),
                    onPressed: () {
                      Clipboard.setData(ClipboardData(text: _dataDir));
                      _showSnack('Copied');
                    },
                  )
                : null,
          ),
          ListTile(
            leading: const Icon(Icons.upload_file),
            title: const Text('Export Profile'),
            subtitle: const Text('Copy profile JSON to clipboard'),
            onTap: _exportConfig,
          ),
          ListTile(
            leading: Icon(Icons.delete_forever, color: theme.colorScheme.error),
            title: Text('Clear All Data', style: TextStyle(color: theme.colorScheme.error)),
            subtitle: const Text('Remove registration and local cache'),
            onTap: _showClearDataDialog,
          ),

          const Divider(),

          // System Section
          _SectionHeader(title: 'System', icon: Icons.computer),
          const ListTile(
            leading: Icon(Icons.login),
            title: Text('Start at Login'),
            subtitle: Text('Launch client when system starts'),
            trailing: _StartAtLoginSwitch(),
          ),
          _SettingTile(
            icon: Icons.info,
            title: 'Platform',
            subtitle: Platform.operatingSystem,
          ),

          const Divider(),

          // About Section (merged from AboutScreen)
          _SectionHeader(title: 'About', icon: Icons.info_outline),
          _SettingTile(
            icon: Icons.app_shortcut,
            title: 'Application',
            subtitle: 'NRE Client',
          ),
          _SettingTile(
            icon: Icons.tag,
            title: 'Version',
            subtitle: _appVersion,
          ),
          _SettingTile(
            icon: Icons.code,
            title: 'Distribution',
            subtitle: 'GitHub Release',
          ),
          const _SettingTile(
            icon: Icons.cloud_off,
            title: 'Container Policy',
            subtitle: 'Client artifacts are not embedded in the control-plane image',
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
    return Switch(
      value: _value,
      onChanged: (value) {
        setState(() => _value = value);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(value
                ? 'Start at login enabled (placeholder)'
                : 'Start at login disabled (placeholder)'),
          ),
        );
      },
    );
  }
}
