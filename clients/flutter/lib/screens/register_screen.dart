import 'package:flutter/material.dart';

class RegisterScreen extends StatefulWidget {
  const RegisterScreen({super.key});

  @override
  State<RegisterScreen> createState() => _RegisterScreenState();
}

class _RegisterScreenState extends State<RegisterScreen> {
  final _formKey = GlobalKey<FormState>();
  final _masterUrl = TextEditingController();
  final _token = TextEditingController();
  final _name = TextEditingController();

  @override
  void dispose() {
    _masterUrl.dispose();
    _token.dispose();
    _name.dispose();
    super.dispose();
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
            FilledButton(
              onPressed: () => _formKey.currentState?.validate(),
              child: const Text('Register'),
            ),
          ],
        ),
      ),
    );
  }
}
