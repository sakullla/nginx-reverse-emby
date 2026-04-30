import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../core/client_state.dart';
import '../services/local_agent_controller.dart';
import '../services/local_agent_controller_factory.dart';
import '../services/master_api.dart';

class AgentScreen extends StatefulWidget {
  const AgentScreen({
    super.key,
    this.api = const HttpMasterApi(),
    this.initialState,
    this.onStateChanged,
    this.generateAgentToken = defaultAgentTokenGenerator,
    this.platform = 'windows',
    this.version = '1',
    this.enableAutoRefresh = true,
    this.controller,
  });

  final MasterApi api;
  final ClientState? initialState;
  final ClientStateChanged? onStateChanged;
  final AgentTokenGenerator generateAgentToken;
  final String platform;
  final String version;
  final bool enableAutoRefresh;
  final LocalAgentController? controller;

  @override
  State<AgentScreen> createState() => _AgentScreenState();
}

class _AgentScreenState extends State<AgentScreen> {
  // Registration state
  final _formKey = GlobalKey<FormState>();
  final _masterUrl = TextEditingController();
  final _token = TextEditingController();
  final _name = TextEditingController();
  bool _submitting = false;
  String _error = '';

  // Agent runtime state
  LocalAgentController? _controller;
  LocalAgentRuntimeSnapshot? _snapshot;
  var _agentLoading = false;
  Timer? _refreshTimer;

  // Logs state
  final _logScrollController = ScrollController();
  String _logs = '';
  var _logsLoading = false;

  ClientState get _currentState => widget.initialState ?? ClientState.empty();

  @override
  void initState() {
    super.initState();
    _initRegistrationFields();
    _initController();
  }

  @override
  void didUpdateWidget(covariant AgentScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    final wasRegistered = oldWidget.initialState?.profile.isRegistered ?? false;
    final isRegistered = widget.initialState?.profile.isRegistered ?? false;
    if (!wasRegistered && isRegistered) {
      _initController();
    }
    if (wasRegistered && !isRegistered) {
      _refreshTimer?.cancel();
      setState(() {
        _snapshot = null;
        _logs = '';
      });
    }
  }

  void _initRegistrationFields() {
    final profile = _currentState.profile;
    _masterUrl.text = profile.masterUrl;
    _name.text = profile.displayName;
  }

  void _initController() {
    _controller = widget.controller ?? createLocalAgentController();
    if (_currentState.profile.isRegistered) {
      _refreshAgentStatus();
      _startAutoRefresh();
    }
  }

  void _startAutoRefresh() {
    if (!widget.enableAutoRefresh) return;
    _refreshTimer?.cancel();
    _refreshTimer = Timer.periodic(const Duration(seconds: 3), (_) {
      if (mounted && _currentState.profile.isRegistered) {
        _refreshAgentStatus(silent: true);
        _refreshLogs(silent: true);
      }
    });
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
    _masterUrl.dispose();
    _token.dispose();
    _name.dispose();
    _logScrollController.dispose();
    super.dispose();
  }

  // ── Registration ──

  Future<void> _register() async {
    if (_submitting || !(_formKey.currentState?.validate() ?? false)) return;

    final masterUrl = normalizeMasterUrl(_masterUrl.text);
    final registerToken = _token.text.trim();
    final name = _name.text.trim().isEmpty ? 'nre-client' : _name.text.trim();
    final agentToken = _existingAgentToken();

    setState(() {
      _submitting = true;
      _error = '';
    });

    try {
      final result = await widget.api.register(
        MasterApiConfig(masterUrl: masterUrl, registerToken: registerToken),
        RegisterClientRequest(
          name: name,
          agentToken: agentToken,
          version: widget.version,
          platform: widget.platform,
        ),
      );
      final nextState = _currentState.copyWith(
        profile: ClientProfile(
          masterUrl: masterUrl,
          displayName: name,
          agentId: result.agentId,
          token: result.agentToken,
        ),
        runtimeStatus: ClientRuntimeStatus.registered,
        lastError: '',
      );
      widget.onStateChanged?.call(nextState);
      if (mounted) {
        _showSnack('Registered agent ${result.agentId}');
        _startAutoRefresh();
        _refreshAgentStatus();
      }
    } on MasterApiException catch (error) {
      if (mounted) setState(() => _error = error.message);
    } catch (error) {
      if (mounted) setState(() => _error = 'Registration failed: $error');
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  Future<void> _unregister() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Unregister Agent'),
        content: const Text('This will remove the local registration. The agent on the master server will need to be re-registered.'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: const Text('Unregister')),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;

    // Stop agent first if running
    if (_snapshot?.status == LocalAgentControllerStatus.running) {
      await _controller?.stop(_currentState.profile);
    }

    final nextState = ClientState.empty();
    widget.onStateChanged?.call(nextState);
    _refreshTimer?.cancel();
    setState(() {
      _snapshot = null;
      _logs = '';
    });
    _showSnack('Unregistered');
  }

  String _existingAgentToken() {
    final existing = _currentState.profile.token.trim();
    if (existing.isNotEmpty) return existing;
    return widget.generateAgentToken();
  }

  // ── Agent Control ──

  Future<void> _refreshAgentStatus({bool silent = false}) async {
    final controller = _controller;
    if (controller == null) return;
    if (!silent) setState(() => _agentLoading = true);
    try {
      final snapshot = await controller.status(_currentState.profile);
      if (mounted) {
        setState(() => _snapshot = snapshot);
        if (!silent) _refreshLogs();
      }
    } catch (err) {
      if (mounted && !silent) {
        setState(() => _snapshot = null);
      }
    } finally {
      if (mounted && !silent) setState(() => _agentLoading = false);
    }
  }

  Future<void> _startAgent() async => _runAgentAction(() => _controller!.start(_currentState.profile), 'started');
  Future<void> _stopAgent() async => _runAgentAction(() => _controller!.stop(_currentState.profile), 'stopped');

  Future<void> _restartAgent() async {
    await _stopAgent();
    await Future.delayed(const Duration(milliseconds: 500));
    await _startAgent();
  }

  Future<void> _runAgentAction(
    Future<LocalAgentRuntimeSnapshot> Function() action,
    String actionName,
  ) async {
    setState(() => _agentLoading = true);
    try {
      final snapshot = await action();
      if (mounted) {
        setState(() => _snapshot = snapshot);
        _showSnack('Agent $actionName');
        _refreshLogs();
      }
    } catch (err) {
      if (mounted) _showSnack('Failed: $err', isError: true);
    } finally {
      if (mounted) setState(() => _agentLoading = false);
    }
  }

  // ── Logs ──

  Future<void> _refreshLogs({bool silent = false}) async {
    final controller = _controller;
    if (controller == null) return;
    if (!silent) setState(() => _logsLoading = true);
    try {
      final logs = await controller.readRecentLogs();
      if (mounted) {
        setState(() => _logs = logs);
        if (_logScrollController.hasClients) {
          _logScrollController.jumpTo(_logScrollController.position.maxScrollExtent);
        }
      }
    } finally {
      if (mounted && !silent) setState(() => _logsLoading = false);
    }
  }

  Future<void> _clearLogs() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('Clear Logs'),
        content: const Text('This only clears the displayed logs. The log file on disk is not affected.'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: const Text('Clear')),
        ],
      ),
    );
    if (confirmed == true && mounted) {
      setState(() => _logs = '');
    }
  }

  // ── UI ──

  void _showSnack(String message, {bool isError = false}) {
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(message),
        backgroundColor: isError ? Theme.of(context).colorScheme.error : null,
        duration: const Duration(seconds: 3),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final profile = _currentState.profile;
    final isRegistered = profile.isRegistered;

    return DefaultTabController(
      length: 2,
      child: Scaffold(
        appBar: AppBar(
          title: const Text('Agent'),
          bottom: isRegistered
              ? const TabBar(
                  tabs: [
                    Tab(icon: Icon(Icons.settings), text: 'Control'),
                    Tab(icon: Icon(Icons.article), text: 'Logs'),
                  ],
                )
              : null,
          actions: [
            if (isRegistered)
              IconButton(
                onPressed: _agentLoading ? null : () => _refreshAgentStatus(),
                icon: _agentLoading
                    ? const SizedBox.square(dimension: 20, child: CircularProgressIndicator(strokeWidth: 2))
                    : const Icon(Icons.refresh),
              ),
          ],
        ),
        body: isRegistered
            ? TabBarView(
                children: [
                  _buildControlTab(theme, profile),
                  _buildLogsTab(theme),
                ],
              )
            : _buildRegistrationForm(theme),
      ),
    );
  }

  // ── Registration Form (shown when not registered) ──

  Widget _buildRegistrationForm(ThemeData theme) {
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Card(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Form(
              key: _formKey,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text('Register Agent', style: theme.textTheme.titleLarge),
                  const SizedBox(height: 8),
                  Text(
                    'Connect this client to a master server. You will need a register token from the server.',
                    style: theme.textTheme.bodyMedium?.copyWith(color: theme.colorScheme.outline),
                  ),
                  const SizedBox(height: 24),
                  TextFormField(
                    controller: _masterUrl,
                    decoration: const InputDecoration(
                      labelText: 'Master URL',
                      hintText: 'https://your-server.com',
                      prefixIcon: Icon(Icons.link),
                      border: OutlineInputBorder(),
                    ),
                    validator: (value) => (value == null || value.trim().isEmpty)
                        ? 'Master URL is required'
                        : null,
                  ),
                  const SizedBox(height: 16),
                  TextFormField(
                    controller: _token,
                    decoration: const InputDecoration(
                      labelText: 'Register token',
                      hintText: 'Enter token from master server',
                      prefixIcon: Icon(Icons.key),
                      border: OutlineInputBorder(),
                    ),
                    obscureText: true,
                    validator: (value) => (value == null || value.trim().isEmpty)
                        ? 'Register token is required'
                        : null,
                  ),
                  const SizedBox(height: 16),
                  TextFormField(
                    controller: _name,
                    decoration: const InputDecoration(
                      labelText: 'Client name',
                      hintText: 'nre-client',
                      prefixIcon: Icon(Icons.badge),
                      border: OutlineInputBorder(),
                    ),
                  ),
                  const SizedBox(height: 24),
                  if (_error.isNotEmpty) ...[
                    Container(
                      padding: const EdgeInsets.all(12),
                      decoration: BoxDecoration(
                        color: theme.colorScheme.errorContainer,
                        borderRadius: BorderRadius.circular(8),
                      ),
                      child: Row(
                        children: [
                          Icon(Icons.error, color: theme.colorScheme.error, size: 18),
                          const SizedBox(width: 8),
                          Expanded(
                            child: Text(
                              _error,
                              style: TextStyle(color: theme.colorScheme.error),
                            ),
                          ),
                        ],
                      ),
                    ),
                    const SizedBox(height: 16),
                  ],
                  SizedBox(
                    width: double.infinity,
                    child: FilledButton(
                      onPressed: _submitting ? null : _register,
                      child: _submitting
                          ? const SizedBox.square(
                              dimension: 18,
                              child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white),
                            )
                          : const Text('Register'),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ],
    );
  }

  // ── Control Tab (shown when registered) ──

  Widget _buildControlTab(ThemeData theme, ClientProfile profile) {
    final snapshot = _snapshot;
    final status = snapshot?.status;
    final isRunning = status == LocalAgentControllerStatus.running;
    final isStopped = status == LocalAgentControllerStatus.stopped;

    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        // Registration Info Card
        Card(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Icon(Icons.link, color: theme.colorScheme.primary),
                    const SizedBox(width: 8),
                    Text('Registration', style: theme.textTheme.titleMedium?.copyWith(fontWeight: FontWeight.bold)),
                  ],
                ),
                const Divider(height: 24),
                _InfoRow(label: 'Master URL', value: profile.masterUrl),
                _InfoRow(label: 'Agent ID', value: profile.agentId),
                _InfoRow(label: 'Display Name', value: profile.displayName.isEmpty ? '-' : profile.displayName),
                _InfoRow(label: 'Token', value: '${profile.token.substring(0, profile.token.length > 8 ? 8 : profile.token.length)}...'),
                const SizedBox(height: 12),
                Wrap(
                  spacing: 8,
                  children: [
                    OutlinedButton.icon(
                      onPressed: () {
                        Clipboard.setData(ClipboardData(text: profile.agentId));
                        _showSnack('Agent ID copied');
                      },
                      icon: const Icon(Icons.copy, size: 16),
                      label: const Text('Copy ID'),
                    ),
                    OutlinedButton.icon(
                      onPressed: _unregister,
                      icon: const Icon(Icons.logout, size: 16),
                      label: const Text('Unregister'),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),

        const SizedBox(height: 16),

        // Agent Process Card
        Card(
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Icon(
                      isRunning ? Icons.play_circle : (isStopped ? Icons.stop_circle : Icons.block),
                      color: isRunning ? Colors.green : (isStopped ? Colors.orange : theme.colorScheme.error),
                    ),
                    const SizedBox(width: 8),
                    Text('Local Agent Process', style: theme.textTheme.titleMedium?.copyWith(fontWeight: FontWeight.bold)),
                    const Spacer(),
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                      decoration: BoxDecoration(
                        color: isRunning ? Colors.green.withValues(alpha: 0.1) : (isStopped ? Colors.orange.withValues(alpha: 0.1) : theme.colorScheme.errorContainer),
                        borderRadius: BorderRadius.circular(12),
                      ),
                      child: Text(
                        _statusLabel(status),
                        style: TextStyle(
                          fontSize: 12,
                          fontWeight: FontWeight.w600,
                          color: isRunning ? Colors.green : (isStopped ? Colors.orange : theme.colorScheme.error),
                        ),
                      ),
                    ),
                  ],
                ),
                const Divider(height: 24),
                if (snapshot != null) ...[
                  _InfoRow(label: 'PID', value: snapshot.pid?.toString() ?? '-'),
                  _InfoRow(label: 'Binary Path', value: snapshot.binaryPath),
                  _InfoRow(label: 'Data Directory', value: snapshot.dataDir),
                  _InfoRow(label: 'Log Path', value: snapshot.logPath),
                  if (snapshot.message.isNotEmpty)
                    _InfoRow(label: 'Message', value: snapshot.message),
                  const SizedBox(height: 16),
                  Row(
                    children: [
                      Expanded(
                        child: FilledButton.icon(
                          onPressed: !_agentLoading && isStopped ? _startAgent : null,
                          icon: const Icon(Icons.play_arrow),
                          label: const Text('Start'),
                        ),
                      ),
                      const SizedBox(width: 8),
                      Expanded(
                        child: FilledButton.tonalIcon(
                          onPressed: !_agentLoading && isRunning ? _stopAgent : null,
                          icon: const Icon(Icons.stop),
                          label: const Text('Stop'),
                        ),
                      ),
                      const SizedBox(width: 8),
                      Expanded(
                        child: OutlinedButton.icon(
                          onPressed: !_agentLoading && isRunning ? _restartAgent : null,
                          icon: const Icon(Icons.restart_alt),
                          label: const Text('Restart'),
                        ),
                      ),
                    ],
                  ),
                ] else ...[
                  const Center(child: Text('Unable to determine agent status')),
                ],
              ],
            ),
          ),
        ),
      ],
    );
  }

  // ── Logs Tab ──

  Widget _buildLogsTab(ThemeData theme) {
    return Column(
      children: [
        Container(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          decoration: BoxDecoration(
            color: theme.colorScheme.surfaceContainerHighest,
            border: Border(bottom: BorderSide(color: theme.dividerColor)),
          ),
          child: Row(
            children: [
              Text('Agent Logs', style: theme.textTheme.titleSmall),
              const Spacer(),
              IconButton(
                onPressed: _logsLoading ? null : () => _refreshLogs(),
                icon: _logsLoading
                    ? const SizedBox.square(dimension: 16, child: CircularProgressIndicator(strokeWidth: 2))
                    : const Icon(Icons.refresh, size: 18),
                tooltip: 'Refresh',
              ),
              IconButton(
                onPressed: _logs.isEmpty ? null : _clearLogs,
                icon: const Icon(Icons.clear_all, size: 18),
                tooltip: 'Clear view',
              ),
            ],
          ),
        ),
        Expanded(
          child: Container(
            color: const Color(0xFF1E1E1E),
            child: _logs.isEmpty
                ? const Center(
                    child: Text(
                      'No logs available.\nStart the agent to see logs.',
                      textAlign: TextAlign.center,
                      style: TextStyle(color: Colors.white54),
                    ),
                  )
                : SingleChildScrollView(
                    controller: _logScrollController,
                    padding: const EdgeInsets.all(12),
                    child: SelectableText(
                      _logs,
                      style: const TextStyle(
                        fontFamily: 'Consolas',
                        fontFamilyFallback: ['monospace'],
                        fontSize: 12,
                        color: Color(0xFFD4D4D4),
                        height: 1.4,
                      ),
                    ),
                  ),
          ),
        ),
      ],
    );
  }

  String _statusLabel(LocalAgentControllerStatus? status) {
    switch (status) {
      case LocalAgentControllerStatus.running:
        return 'Running';
      case LocalAgentControllerStatus.stopped:
        return 'Stopped';
      case LocalAgentControllerStatus.unavailable:
        return 'Unavailable';
      case null:
        return 'Checking';
    }
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
