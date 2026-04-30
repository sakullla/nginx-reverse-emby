import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../core/client_state.dart';
import '../l10n/app_localizations.dart';
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
  final _formKey = GlobalKey<FormState>();
  final _masterUrl = TextEditingController();
  final _token = TextEditingController();
  final _name = TextEditingController();
  bool _submitting = false;
  String _error = '';

  LocalAgentController? _controller;
  LocalAgentRuntimeSnapshot? _snapshot;
  var _agentLoading = false;
  Timer? _refreshTimer;

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

  Future<void> _register() async {
    final l10n = AppLocalizations.of(context)!;
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
        _showSnack(l10n.msgRegistered(result.agentId));
        _startAutoRefresh();
        _refreshAgentStatus();
      }
    } on MasterApiException catch (error) {
      if (mounted) setState(() => _error = error.message);
    } catch (error) {
      if (mounted) setState(() => _error = l10n.errorRegistrationFailed(error.toString()));
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  Future<void> _unregister() async {
    final l10n = AppLocalizations.of(context)!;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(l10n.titleUnregisterAgent),
        content: Text(l10n.descUnregisterConfirm),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: Text(l10n.btnCancel)),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: Text(l10n.btnUnregister)),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;

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
    _showSnack(l10n.msgUnregistered);
  }

  String _existingAgentToken() {
    final existing = _currentState.profile.token.trim();
    if (existing.isNotEmpty) return existing;
    return widget.generateAgentToken();
  }

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
    final l10n = AppLocalizations.of(context)!;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(l10n.titleClearLogs),
        content: Text(l10n.descClearLogs),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: Text(l10n.btnCancel)),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: Text(l10n.btnClear)),
        ],
      ),
    );
    if (confirmed == true && mounted) {
      setState(() => _logs = '');
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

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l10n = AppLocalizations.of(context)!;
    final profile = _currentState.profile;
    final isRegistered = profile.isRegistered;

    return DefaultTabController(
      length: 2,
      child: Scaffold(
        appBar: AppBar(
          title: Text(l10n.titleAgent),
          bottom: isRegistered
              ? TabBar(
                  tabs: [
                    Tab(icon: const Icon(Icons.settings), text: l10n.titleControl),
                    Tab(icon: const Icon(Icons.article), text: l10n.titleLogs),
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
                  _buildControlTab(theme, profile, l10n),
                  _buildLogsTab(theme, l10n),
                ],
              )
            : _buildRegistrationForm(theme, l10n),
      ),
    );
  }

  Widget _buildRegistrationForm(ThemeData theme, AppLocalizations l10n) {
    return Center(
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 560),
        child: ListView(
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
                  Text(l10n.titleRegisterAgent, style: theme.textTheme.titleLarge),
                  const SizedBox(height: 8),
                  Text(
                    l10n.descRegisterAgent,
                    style: theme.textTheme.bodyMedium?.copyWith(color: theme.colorScheme.outline),
                  ),
                  const SizedBox(height: 24),
                  TextFormField(
                    controller: _masterUrl,
                    decoration: InputDecoration(
                      labelText: l10n.labelMasterUrl,
                      hintText: l10n.hintMasterUrl,
                      prefixIcon: const Icon(Icons.link),
                    ),
                    validator: (value) => (value == null || value.trim().isEmpty)
                        ? l10n.errorRequiredMasterUrl
                        : null,
                  ),
                  const SizedBox(height: 16),
                  TextFormField(
                    controller: _token,
                    decoration: InputDecoration(
                      labelText: l10n.labelRegisterToken,
                      hintText: l10n.hintRegisterToken,
                      prefixIcon: const Icon(Icons.key),
                    ),
                    obscureText: true,
                    validator: (value) => (value == null || value.trim().isEmpty)
                        ? l10n.errorRequiredRegisterToken
                        : null,
                  ),
                  const SizedBox(height: 16),
                  TextFormField(
                    controller: _name,
                    decoration: InputDecoration(
                      labelText: l10n.labelClientName,
                      hintText: l10n.hintClientName,
                      prefixIcon: const Icon(Icons.badge),
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
                          : Text(l10n.btnRegister),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ],
    ),
  ),
);
}

  Widget _buildControlTab(ThemeData theme, ClientProfile profile, AppLocalizations l10n) {
    final snapshot = _snapshot;
    final status = snapshot?.status;
    final isRunning = status == LocalAgentControllerStatus.running;
    final isStopped = status == LocalAgentControllerStatus.stopped;

    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
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
                    Text(l10n.titleRegistration, style: theme.textTheme.titleMedium?.copyWith(fontWeight: FontWeight.bold)),
                  ],
                ),
                const Divider(height: 24),
                _InfoRow(label: l10n.labelMasterUrl, value: profile.masterUrl),
                _InfoRow(label: l10n.labelAgentId, value: profile.agentId),
                _InfoRow(label: l10n.labelDisplayName, value: profile.displayName.isEmpty ? l10n.valueDash : profile.displayName),
                _InfoRow(label: l10n.labelToken, value: '${profile.token.substring(0, profile.token.length > 8 ? 8 : profile.token.length)}...'),
                const SizedBox(height: 12),
                Wrap(
                  spacing: 8,
                  children: [
                    OutlinedButton.icon(
                      onPressed: () {
                        Clipboard.setData(ClipboardData(text: profile.agentId));
                        _showSnack(l10n.msgCopied);
                      },
                      icon: const Icon(Icons.copy, size: 16),
                      label: Text(l10n.btnCopyId),
                    ),
                    OutlinedButton.icon(
                      onPressed: _unregister,
                      icon: const Icon(Icons.logout, size: 16),
                      label: Text(l10n.btnUnregister),
                    ),
                  ],
                ),
              ],
            ),
          ),
        ),

        const SizedBox(height: 16),

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
                    Text(l10n.titleLocalAgentProcess, style: theme.textTheme.titleMedium?.copyWith(fontWeight: FontWeight.bold)),
                    const Spacer(),
                    Container(
                      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
                      decoration: BoxDecoration(
                        color: isRunning ? Colors.green.withValues(alpha: 0.1) : (isStopped ? Colors.orange.withValues(alpha: 0.1) : theme.colorScheme.errorContainer),
                        borderRadius: BorderRadius.circular(12),
                      ),
                      child: Text(
                        _statusLabel(status, l10n),
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
                  _InfoRow(label: l10n.labelPid, value: snapshot.pid?.toString() ?? l10n.valueDash),
                  _InfoRow(label: l10n.labelBinaryPath, value: snapshot.binaryPath),
                  _InfoRow(label: l10n.labelDataDir, value: snapshot.dataDir),
                  _InfoRow(label: l10n.labelLogPath, value: snapshot.logPath),
                  if (snapshot.message.isNotEmpty)
                    _InfoRow(label: l10n.labelMessage, value: snapshot.message),
                  const SizedBox(height: 16),
                  Row(
                    children: [
                      Expanded(
                        child: FilledButton.icon(
                          onPressed: !_agentLoading && isStopped ? _startAgent : null,
                          icon: const Icon(Icons.play_arrow),
                          label: Text(l10n.btnStart),
                        ),
                      ),
                      const SizedBox(width: 8),
                      Expanded(
                        child: FilledButton.tonalIcon(
                          onPressed: !_agentLoading && isRunning ? _stopAgent : null,
                          icon: const Icon(Icons.stop),
                          label: Text(l10n.btnStop),
                        ),
                      ),
                      const SizedBox(width: 8),
                      Expanded(
                        child: OutlinedButton.icon(
                          onPressed: !_agentLoading && isRunning ? _restartAgent : null,
                          icon: const Icon(Icons.restart_alt),
                          label: Text(l10n.btnRestart),
                        ),
                      ),
                    ],
                  ),
                ] else ...[
                  Center(child: Text(l10n.descUnableDetermineStatus)),
                ],
              ],
            ),
          ),
        ),
      ],
    );
  }

  Widget _buildLogsTab(ThemeData theme, AppLocalizations l10n) {
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
              Text(l10n.titleAgentLogs, style: theme.textTheme.titleSmall),
              const Spacer(),
              IconButton(
                onPressed: _logsLoading ? null : () => _refreshLogs(),
                icon: _logsLoading
                    ? const SizedBox.square(dimension: 16, child: CircularProgressIndicator(strokeWidth: 2))
                    : const Icon(Icons.refresh, size: 18),
                tooltip: l10n.btnRefresh,
              ),
              IconButton(
                onPressed: _logs.isEmpty ? null : _clearLogs,
                icon: const Icon(Icons.clear_all, size: 18),
                tooltip: l10n.btnClear,
              ),
            ],
          ),
        ),
        Expanded(
          child: Container(
            color: const Color(0xFF1E1E1E),
            child: _logs.isEmpty
                ? Center(
                    child: Text(
                      l10n.msgNoLogs,
                      textAlign: TextAlign.center,
                      style: const TextStyle(color: Colors.white54),
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

  String _statusLabel(LocalAgentControllerStatus? status, AppLocalizations l10n) {
    switch (status) {
      case LocalAgentControllerStatus.running:
        return l10n.statusRunning;
      case LocalAgentControllerStatus.stopped:
        return l10n.statusStopped;
      case LocalAgentControllerStatus.unavailable:
        return l10n.statusUnavailable;
      case null:
        return l10n.statusChecking;
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
