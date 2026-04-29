import 'package:flutter/material.dart';

import '../core/client_state.dart';
import '../services/local_agent_controller.dart';

class RuntimeScreen extends StatefulWidget {
  const RuntimeScreen({
    super.key,
    required this.state,
    required this.controller,
  });

  final ClientState state;
  final LocalAgentController controller;

  @override
  State<RuntimeScreen> createState() => _RuntimeScreenState();
}

class _RuntimeScreenState extends State<RuntimeScreen> {
  LocalAgentRuntimeSnapshot? _snapshot;
  var _busy = false;
  var _error = '';

  @override
  void initState() {
    super.initState();
    _refresh();
  }

  @override
  void didUpdateWidget(RuntimeScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.state.profile.agentId != widget.state.profile.agentId ||
        oldWidget.state.profile.token != widget.state.profile.token) {
      _refresh();
    }
  }

  Future<void> _refresh() async {
    setState(() {
      _busy = true;
      _error = '';
    });
    try {
      final snapshot = await widget.controller.status(widget.state.profile);
      if (!mounted) return;
      setState(() => _snapshot = snapshot);
    } catch (err) {
      if (!mounted) return;
      setState(() => _error = _cleanError(err));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  Future<void> _start() async {
    await _runAction(() => widget.controller.start(widget.state.profile));
  }

  Future<void> _stop() async {
    await _runAction(() => widget.controller.stop(widget.state.profile));
  }

  Future<void> _runAction(
    Future<LocalAgentRuntimeSnapshot> Function() action,
  ) async {
    setState(() {
      _busy = true;
      _error = '';
    });
    try {
      final snapshot = await action();
      if (!mounted) return;
      setState(() => _snapshot = snapshot);
    } catch (err) {
      if (!mounted) return;
      setState(() => _error = _cleanError(err));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final snapshot = _snapshot;
    final status = snapshot?.status;
    final isRunning = status == LocalAgentControllerStatus.running;
    final isStopped = status == LocalAgentControllerStatus.stopped;

    return Scaffold(
      appBar: AppBar(
        title: const Text('Runtime'),
        actions: [
          IconButton(
            tooltip: 'Refresh',
            onPressed: _busy ? null : _refresh,
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: ListView(
        padding: const EdgeInsets.all(16),
        children: [
          ListTile(
            title: const Text('Local agent'),
            subtitle: Text(_statusLabel(status)),
            trailing: _busy ? const CircularProgressIndicator() : null,
          ),
          if (snapshot?.pid != null)
            ListTile(
              title: const Text('Process'),
              subtitle: Text('PID ${snapshot!.pid}'),
            ),
          if ((snapshot?.binaryPath ?? '').isNotEmpty)
            ListTile(
              title: const Text('Binary'),
              subtitle: Text(snapshot!.binaryPath),
            ),
          if ((snapshot?.dataDir ?? '').isNotEmpty)
            ListTile(
              title: const Text('Data directory'),
              subtitle: Text(snapshot!.dataDir),
            ),
          if ((snapshot?.logPath ?? '').isNotEmpty)
            ListTile(
              title: const Text('Log'),
              subtitle: Text(snapshot!.logPath),
            ),
          if ((snapshot?.message ?? '').isNotEmpty)
            ListTile(
              title: const Text('Message'),
              subtitle: Text(snapshot!.message),
            ),
          if (_error.isNotEmpty)
            ListTile(title: const Text('Error'), subtitle: Text(_error)),
          FilledButton(
            onPressed: !_busy && isStopped ? _start : null,
            child: const Text('Start Agent'),
          ),
          const SizedBox(height: 8),
          OutlinedButton(
            onPressed: !_busy && isRunning ? _stop : null,
            child: const Text('Stop Agent'),
          ),
        ],
      ),
    );
  }
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

String _cleanError(Object err) {
  final message = err.toString();
  return message.startsWith('Exception: ')
      ? message.substring('Exception: '.length)
      : message;
}
