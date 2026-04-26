import 'dart:math';

import 'package:flutter/material.dart';

import '../core/client_state.dart';
import '../services/master_api.dart';

typedef ClientStateChanged = void Function(ClientState state);
typedef AgentTokenGenerator = String Function();

class RegisterScreen extends StatefulWidget {
  const RegisterScreen({
    super.key,
    this.api = const HttpMasterApi(),
    this.initialState,
    this.onStateChanged,
    this.generateAgentToken = defaultAgentTokenGenerator,
    this.platform = 'android',
    this.version = '1',
  });

  final MasterApi api;
  final ClientState? initialState;
  final ClientStateChanged? onStateChanged;
  final AgentTokenGenerator generateAgentToken;
  final String platform;
  final String version;

  @override
  State<RegisterScreen> createState() => _RegisterScreenState();
}

class _RegisterScreenState extends State<RegisterScreen> {
  final _formKey = GlobalKey<FormState>();
  final _masterUrl = TextEditingController();
  final _token = TextEditingController();
  final _name = TextEditingController();
  bool _submitting = false;
  String _error = '';
  String _success = '';

  @override
  void initState() {
    super.initState();
    final profile = widget.initialState?.profile;
    if (profile != null) {
      _masterUrl.text = profile.masterUrl;
      _name.text = profile.displayName;
    }
  }

  @override
  void dispose() {
    _masterUrl.dispose();
    _token.dispose();
    _name.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (_submitting || !(_formKey.currentState?.validate() ?? false)) {
      return;
    }

    final masterUrl = normalizeMasterUrl(_masterUrl.text);
    final registerToken = _token.text.trim();
    final name = _name.text.trim().isEmpty ? 'nre-client' : _name.text.trim();
    final agentToken = _existingAgentToken();

    setState(() {
      _submitting = true;
      _error = '';
      _success = '';
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
      final nextState = (widget.initialState ?? ClientState.empty()).copyWith(
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
      if (!mounted) {
        return;
      }
      setState(() {
        _success = 'Registered agent ${result.agentId}';
      });
    } on MasterApiException catch (error) {
      if (!mounted) {
        return;
      }
      setState(() {
        _error = error.message;
      });
    } catch (error) {
      if (!mounted) {
        return;
      }
      setState(() {
        _error = 'Registration failed: $error';
      });
    } finally {
      if (mounted) {
        setState(() {
          _submitting = false;
        });
      }
    }
  }

  String _existingAgentToken() {
    final existing = widget.initialState?.profile.token.trim() ?? '';
    if (existing.isNotEmpty) {
      return existing;
    }
    return widget.generateAgentToken();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Register')),
      body: Form(
        key: _formKey,
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            TextFormField(
              controller: _masterUrl,
              decoration: const InputDecoration(labelText: 'Master URL'),
              validator: (value) => (value == null || value.trim().isEmpty)
                  ? 'Master URL is required'
                  : null,
            ),
            TextFormField(
              controller: _token,
              decoration: const InputDecoration(labelText: 'Register token'),
              obscureText: true,
              validator: (value) => (value == null || value.trim().isEmpty)
                  ? 'Register token is required'
                  : null,
            ),
            TextFormField(
              controller: _name,
              decoration: const InputDecoration(labelText: 'Client name'),
            ),
            const SizedBox(height: 16),
            if (_error.isNotEmpty) ...[
              Text(
                _error,
                style: TextStyle(color: Theme.of(context).colorScheme.error),
              ),
              const SizedBox(height: 12),
            ],
            if (_success.isNotEmpty) ...[
              Text(
                _success,
                style: TextStyle(color: Theme.of(context).colorScheme.primary),
              ),
              const SizedBox(height: 12),
            ],
            FilledButton(
              onPressed: _submitting ? null : _submit,
              child: _submitting
                  ? const SizedBox.square(
                      dimension: 18,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Text('Register'),
            ),
          ],
        ),
      ),
    );
  }
}

String defaultAgentTokenGenerator() {
  final random = Random.secure();
  final bytes = List<int>.generate(24, (_) => random.nextInt(256));
  return bytes.map((byte) => byte.toRadixString(16).padLeft(2, '0')).join();
}
