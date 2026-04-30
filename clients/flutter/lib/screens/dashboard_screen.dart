import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../core/client_state.dart';
import '../l10n/app_localizations.dart';
import '../services/local_agent_controller.dart';

class DashboardScreen extends StatefulWidget {
  const DashboardScreen({
    super.key,
    required this.state,
    required this.controller,
    this.onNavigateToAgent,
    this.onNavigateToRegistration,
  });

  final ClientState state;
  final LocalAgentController controller;
  final VoidCallback? onNavigateToAgent;
  final VoidCallback? onNavigateToRegistration;

  @override
  State<DashboardScreen> createState() => _DashboardScreenState();
}

class _DashboardScreenState extends State<DashboardScreen> {
  LocalAgentRuntimeSnapshot? _snapshot;
  var _loading = true;
  var _lastCheck = DateTime.now();

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  @override
  void didUpdateWidget(covariant DashboardScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.state.profile.agentId != widget.state.profile.agentId ||
        oldWidget.state.profile.token != widget.state.profile.token) {
      _refresh();
    }
  }

  Future<void> _refresh() async {
    setState(() => _loading = true);
    try {
      final snapshot = await widget.controller.status(widget.state.profile);
      if (mounted) {
        setState(() {
          _snapshot = snapshot;
          _lastCheck = DateTime.now();
          _loading = false;
        });
      }
    } catch (_) {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final colorScheme = theme.colorScheme;
    final l10n = AppLocalizations.of(context)!;
    final profile = widget.state.profile;
    final isRegistered = profile.isRegistered;
    final snapshot = _snapshot;

    return Scaffold(
      appBar: AppBar(
        title: Text(l10n.titleDashboard),
        actions: [
          IconButton(
            onPressed: _loading ? null : _refresh,
            icon: _loading
                ? const SizedBox.square(
                    dimension: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Icon(Icons.refresh),
          ),
        ],
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          _StatusCard(
            title: l10n.titleConnection,
            icon: isRegistered ? Icons.check_circle : Icons.error,
            iconColor: isRegistered ? Colors.green : colorScheme.error,
            subtitle: isRegistered ? l10n.statusRegistered : l10n.statusNotRegistered,
            children: [
              if (isRegistered) ...[
                _InfoRow(label: l10n.labelMasterUrl, value: profile.masterUrl),
                _InfoRow(label: l10n.labelAgentId, value: profile.agentId),
                _InfoRow(label: l10n.labelDisplayName, value: profile.displayName.isEmpty ? l10n.valueDash : profile.displayName),
              ] else ...[
                Text(l10n.descRegisterClient, style: theme.textTheme.bodyMedium?.copyWith(color: colorScheme.outline)),
                const SizedBox(height: 12),
                FilledButton(
                  onPressed: widget.onNavigateToRegistration,
                  child: Text(l10n.btnRegisterNow),
                ),
              ],
            ],
          ),

          const SizedBox(height: 16),

          _StatusCard(
            title: l10n.titleLocalAgent,
            icon: _agentStatusIcon(snapshot?.status),
            iconColor: _agentStatusColor(snapshot?.status, colorScheme),
            subtitle: _agentStatusText(snapshot?.status, l10n),
            children: [
              if (snapshot != null) ...[
                _InfoRow(label: l10n.labelPid, value: snapshot.pid?.toString() ?? l10n.valueDash),
                _InfoRow(label: l10n.labelBinaryPath, value: snapshot.binaryPath),
                _InfoRow(label: l10n.labelDataDir, value: snapshot.dataDir),
                if (snapshot.message.isNotEmpty)
                  _InfoRow(label: l10n.labelMessage, value: snapshot.message),
                const SizedBox(height: 12),
                Wrap(
                  spacing: 8,
                  children: [
                    if (snapshot.canStart)
                      FilledButton(
                        onPressed: () => _startAgent(l10n),
                        child: Text(l10n.btnStart),
                      ),
                    if (snapshot.canStop)
                      FilledButton.tonal(
                        onPressed: () => _stopAgent(l10n),
                        child: Text(l10n.btnStop),
                      ),
                    OutlinedButton(
                      onPressed: widget.onNavigateToAgent,
                      child: Text(l10n.btnViewDetails),
                    ),
                  ],
                ),
              ] else if (!_loading) ...[
                Text(l10n.descUnableDetermineStatus, style: theme.textTheme.bodyMedium?.copyWith(color: colorScheme.outline)),
              ],
            ],
          ),

          const SizedBox(height: 16),

          _StatusCard(
            title: l10n.titleOverview,
            icon: Icons.analytics,
            iconColor: colorScheme.primary,
            subtitle: l10n.msgLastUpdated(DateFormat('HH:mm:ss').format(_lastCheck)),
            children: [
              _StatRow(
                icon: Icons.dns,
                label: l10n.labelMasterUrl,
                value: profile.masterUrl.isEmpty ? l10n.labelNotConfigured : profile.masterUrl,
              ),
              _StatRow(
                icon: Icons.badge,
                label: l10n.labelAgentId,
                value: profile.agentId.isEmpty ? l10n.valueDash : profile.agentId,
              ),
              _StatRow(
                icon: Icons.memory,
                label: l10n.labelAgentStatus,
                value: _agentStatusText(snapshot?.status, l10n),
              ),
              _StatRow(
                icon: Icons.info,
                label: l10n.labelPlatform,
                value: widget.state.platform,
              ),
            ],
          ),
        ],
      ),
    );
  }

  Future<void> _startAgent(AppLocalizations l10n) async {
    try {
      final s = await widget.controller.start(widget.state.profile);
      if (mounted) {
        setState(() => _snapshot = s);
        _showSnack(l10n.msgAgentStarted(s.pid?.toString() ?? l10n.valueDash));
      }
    } catch (e) {
      if (mounted) _showSnack(l10n.msgActionFailed(e.toString()), isError: true);
    }
  }

  Future<void> _stopAgent(AppLocalizations l10n) async {
    try {
      final s = await widget.controller.stop(widget.state.profile);
      if (mounted) {
        setState(() => _snapshot = s);
        _showSnack(l10n.msgAgentStopped);
      }
    } catch (e) {
      if (mounted) _showSnack(l10n.msgActionFailed(e.toString()), isError: true);
    }
  }

  void _showSnack(String message, {bool isError = false}) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(message),
        backgroundColor: isError ? Theme.of(context).colorScheme.errorContainer : null,
        duration: const Duration(seconds: 3),
      ),
    );
  }

  IconData _agentStatusIcon(LocalAgentControllerStatus? status) {
    switch (status) {
      case LocalAgentControllerStatus.running:
        return Icons.play_circle;
      case LocalAgentControllerStatus.stopped:
        return Icons.stop_circle;
      case LocalAgentControllerStatus.unavailable:
        return Icons.block;
      case null:
        return Icons.help_outline;
    }
  }

  Color _agentStatusColor(LocalAgentControllerStatus? status, ColorScheme scheme) {
    switch (status) {
      case LocalAgentControllerStatus.running:
        return Colors.green;
      case LocalAgentControllerStatus.stopped:
        return Colors.orange;
      case LocalAgentControllerStatus.unavailable:
        return scheme.error;
      case null:
        return scheme.outline;
    }
  }

  String _agentStatusText(LocalAgentControllerStatus? status, AppLocalizations l10n) {
    switch (status) {
      case LocalAgentControllerStatus.running:
        return l10n.statusRunning;
      case LocalAgentControllerStatus.stopped:
        return l10n.statusStopped;
      case LocalAgentControllerStatus.unavailable:
        return l10n.statusUnavailable;
      case null:
        return l10n.statusUnknown;
    }
  }
}

class _StatusCard extends StatelessWidget {
  const _StatusCard({
    required this.title,
    required this.icon,
    required this.iconColor,
    required this.subtitle,
    required this.children,
  });

  final String title;
  final IconData icon;
  final Color iconColor;
  final String subtitle;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Card(
      clipBehavior: Clip.antiAlias,
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(icon, color: iconColor),
                const SizedBox(width: 8),
                Text(title, style: theme.textTheme.titleMedium?.copyWith(fontWeight: FontWeight.bold)),
                const Spacer(),
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                  decoration: BoxDecoration(
                    color: iconColor.withValues(alpha: 0.1),
                    borderRadius: BorderRadius.circular(20),
                  ),
                  child: Text(
                    subtitle,
                    style: theme.textTheme.bodySmall?.copyWith(
                      color: iconColor,
                      fontWeight: FontWeight.w600,
                    ),
                  ),
                ),
              ],
            ),
            const Divider(height: 24),
            ...children,
          ],
        ),
      ),
    );
  }
}

class _InfoRow extends StatelessWidget {
  const _InfoRow({required this.label, required this.value});

  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 120,
            child: Text(
              label,
              style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.outline),
            ),
          ),
          Expanded(
            child: SelectableText(
              value,
              style: theme.textTheme.bodyMedium,
            ),
          ),
        ],
      ),
    );
  }
}

class _StatRow extends StatelessWidget {
  const _StatRow({required this.icon, required this.label, required this.value});

  final IconData icon;
  final String label;
  final String value;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Row(
        children: [
          Icon(icon, size: 18, color: theme.colorScheme.primary),
          const SizedBox(width: 12),
          Expanded(
            flex: 2,
            child: Text(label, style: theme.textTheme.bodyMedium),
          ),
          Expanded(
            flex: 3,
            child: Text(
              value,
              style: theme.textTheme.bodyMedium?.copyWith(fontWeight: FontWeight.w500),
              textAlign: TextAlign.right,
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ],
      ),
    );
  }
}
