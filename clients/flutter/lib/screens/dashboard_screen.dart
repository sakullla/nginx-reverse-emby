import 'package:flutter/material.dart';
import 'package:intl/intl.dart';

import '../core/client_state.dart';
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
    final profile = widget.state.profile;
    final isRegistered = profile.isRegistered;
    final snapshot = _snapshot;

    return Scaffold(
      appBar: AppBar(
        title: const Text('Dashboard'),
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
          // Connection Status Card
          _StatusCard(
            title: 'Connection',
            icon: isRegistered ? Icons.check_circle : Icons.error,
            iconColor: isRegistered ? Colors.green : colorScheme.error,
            subtitle: isRegistered ? 'Registered' : 'Not registered',
            children: [
              if (isRegistered) ...[
                _InfoRow(label: 'Master URL', value: profile.masterUrl),
                _InfoRow(label: 'Agent ID', value: profile.agentId),
                _InfoRow(label: 'Display Name', value: profile.displayName.isEmpty ? '-' : profile.displayName),
              ] else ...[
                const Text('Register this client to connect to a master server.'),
                const SizedBox(height: 12),
                FilledButton(
                  onPressed: widget.onNavigateToRegistration,
                  child: const Text('Register Now'),
                ),
              ],
            ],
          ),

          const SizedBox(height: 16),

          // Agent Status Card
          _StatusCard(
            title: 'Local Agent',
            icon: _agentStatusIcon(snapshot?.status),
            iconColor: _agentStatusColor(snapshot?.status, colorScheme),
            subtitle: _agentStatusText(snapshot?.status),
            children: [
              if (snapshot != null) ...[
                _InfoRow(label: 'PID', value: snapshot.pid?.toString() ?? '-'),
                _InfoRow(label: 'Binary', value: snapshot.binaryPath),
                _InfoRow(label: 'Data Directory', value: snapshot.dataDir),
                if (snapshot.message.isNotEmpty)
                  _InfoRow(label: 'Message', value: snapshot.message),
                const SizedBox(height: 12),
                Wrap(
                  spacing: 8,
                  children: [
                    if (snapshot.canStart)
                      FilledButton(
                        onPressed: () => _startAgent(),
                        child: const Text('Start Agent'),
                      ),
                    if (snapshot.canStop)
                      FilledButton.tonal(
                        onPressed: () => _stopAgent(),
                        child: const Text('Stop Agent'),
                      ),
                    OutlinedButton(
                      onPressed: widget.onNavigateToAgent,
                      child: const Text('View Details'),
                    ),
                  ],
                ),
              ] else if (!_loading) ...[
                const Text('Unable to determine agent status.'),
              ],
            ],
          ),

          const SizedBox(height: 16),

          // Quick Stats Card
          _StatusCard(
            title: 'Overview',
            icon: Icons.analytics,
            iconColor: colorScheme.primary,
            subtitle: 'Last updated: ${DateFormat('HH:mm:ss').format(_lastCheck)}',
            children: [
              _StatRow(
                icon: Icons.dns,
                label: 'Master URL',
                value: profile.masterUrl.isEmpty ? 'Not configured' : profile.masterUrl,
              ),
              _StatRow(
                icon: Icons.badge,
                label: 'Agent ID',
                value: profile.agentId.isEmpty ? '-' : profile.agentId,
              ),
              _StatRow(
                icon: Icons.memory,
                label: 'Agent Status',
                value: _agentStatusText(snapshot?.status),
              ),
              _StatRow(
                icon: Icons.info,
                label: 'Platform',
                value: widget.state.platform,
              ),
            ],
          ),
        ],
      ),
    );
  }

  Future<void> _startAgent() async {
    try {
      final s = await widget.controller.start(widget.state.profile);
      if (mounted) {
        setState(() => _snapshot = s);
        _showSnack('Agent started (PID: ${s.pid})');
      }
    } catch (e) {
      if (mounted) _showSnack('Failed to start agent: $e', isError: true);
    }
  }

  Future<void> _stopAgent() async {
    try {
      final s = await widget.controller.stop(widget.state.profile);
      if (mounted) {
        setState(() => _snapshot = s);
        _showSnack('Agent stopped');
      }
    } catch (e) {
      if (mounted) _showSnack('Failed to stop agent: $e', isError: true);
    }
  }

  void _showSnack(String message, {bool isError = false}) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(message),
        backgroundColor: isError ? Theme.of(context).colorScheme.error : null,
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

  String _agentStatusText(LocalAgentControllerStatus? status) {
    switch (status) {
      case LocalAgentControllerStatus.running:
        return 'Running';
      case LocalAgentControllerStatus.stopped:
        return 'Stopped';
      case LocalAgentControllerStatus.unavailable:
        return 'Unavailable';
      case null:
        return 'Unknown';
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
                Text(
                  subtitle,
                  style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.outline),
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
