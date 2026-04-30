import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../data/models/auth_models.dart';
import '../providers/auth_provider.dart';

class ConnectScreen extends ConsumerStatefulWidget {
  const ConnectScreen({super.key});

  @override
  ConsumerState<ConnectScreen> createState() => _ConnectScreenState();
}

class _ConnectScreenState extends ConsumerState<ConnectScreen> {
  final _formKey = GlobalKey<FormState>();
  final _urlController = TextEditingController();
  final _tokenController = TextEditingController();
  final _nameController = TextEditingController(text: 'nre-client');
  var _step = 0;

  @override
  void dispose() {
    _urlController.dispose();
    _tokenController.dispose();
    _nameController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final authAsync = ref.watch(authNotifierProvider);

    return Scaffold(
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 480),
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Card(
              child: Padding(
                padding: const EdgeInsets.all(24),
                child: Form(
                  key: _formKey,
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        'Connect to Master',
                        style: theme.textTheme.headlineSmall?.copyWith(
                          fontWeight: FontWeight.bold,
                        ),
                      ),
                      const SizedBox(height: 8),
                      Text(
                        'Step ${_step + 1} of 3',
                        style: theme.textTheme.bodyMedium?.copyWith(
                          color: theme.colorScheme.outline,
                        ),
                      ),
                      const SizedBox(height: 24),
                      if (_step == 0) ...[
                        TextFormField(
                          controller: _urlController,
                          decoration: const InputDecoration(
                            labelText: 'Master URL',
                            hintText: 'https://your-server.com',
                            prefixIcon: Icon(Icons.link),
                          ),
                          validator: (v) => v == null || v.isEmpty ? 'Please enter URL' : null,
                        ),
                      ] else if (_step == 1) ...[
                        TextFormField(
                          controller: _tokenController,
                          decoration: const InputDecoration(
                            labelText: 'Register Token',
                            hintText: 'Registration token from server',
                            prefixIcon: Icon(Icons.key),
                          ),
                          obscureText: true,
                          validator: (v) => v == null || v.isEmpty ? 'Please enter Token' : null,
                        ),
                      ] else ...[
                        TextFormField(
                          controller: _nameController,
                          decoration: const InputDecoration(
                            labelText: 'Client Name',
                            hintText: 'nre-client',
                            prefixIcon: Icon(Icons.badge),
                          ),
                        ),
                      ],
                      if (authAsync.value is AuthStateError) ...[
                        const SizedBox(height: 16),
                        Text(
                          (authAsync.value as AuthStateError).message,
                          style: TextStyle(color: theme.colorScheme.error),
                        ),
                      ],
                      const SizedBox(height: 24),
                      Row(
                        children: [
                          if (_step > 0)
                            OutlinedButton(
                              onPressed: () => setState(() => _step--),
                              child: const Text('Previous'),
                            ),
                          const Spacer(),
                          FilledButton(
                            onPressed: authAsync.isLoading ? null : _onNext,
                            child: authAsync.isLoading
                                ? const SizedBox.square(
                                    dimension: 18,
                                    child: CircularProgressIndicator(strokeWidth: 2),
                                  )
                                : Text(_step < 2 ? 'Next' : 'Connect'),
                          ),
                        ],
                      ),
                    ],
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  void _onNext() {
    if (!(_formKey.currentState?.validate() ?? false)) return;
    if (_step < 2) {
      setState(() => _step++);
    } else {
      ref.read(authNotifierProvider.notifier).register(
        masterUrl: _urlController.text.trim(),
        registerToken: _tokenController.text.trim(),
        name: _nameController.text.trim(),
      );
    }
  }
}
