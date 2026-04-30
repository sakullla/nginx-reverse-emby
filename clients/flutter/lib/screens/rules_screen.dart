import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';

import '../core/client_state.dart';
import '../l10n/app_localizations.dart';

class ProxyRule {
  const ProxyRule({
    required this.id,
    required this.domain,
    required this.target,
    this.enabled = true,
    this.type = 'http',
  });

  factory ProxyRule.fromJson(Map<String, dynamic> json) {
    return ProxyRule(
      id: json['id']?.toString() ?? '',
      domain: json['domain']?.toString() ?? json['host']?.toString() ?? '',
      target: json['target']?.toString() ?? json['upstream']?.toString() ?? '',
      enabled: json['enabled'] ?? json['active'] ?? true,
      type: json['type']?.toString() ?? 'http',
    );
  }

  final String id;
  final String domain;
  final String target;
  final bool enabled;
  final String type;
}

class RulesScreen extends StatefulWidget {
  const RulesScreen({
    super.key,
    required this.state,
  });

  final ClientState state;

  @override
  State<RulesScreen> createState() => _RulesScreenState();
}

class _RulesScreenState extends State<RulesScreen> {
  List<ProxyRule> _rules = [];
  var _loading = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    if (widget.state.profile.isRegistered) {
      _fetchRules();
    }
  }

  Future<void> _fetchRules() async {
    setState(() {
      _loading = true;
      _error = null;
    });

    try {
      final masterUrl = widget.state.profile.masterUrl;
      final uri = Uri.parse('$masterUrl/api/rules');
      final client = HttpClient();
      try {
        final request = await client.getUrl(uri);
        request.headers.set('Authorization', 'Bearer ${widget.state.profile.token}');
        final response = await request.close();
        final body = await utf8.decoder.bind(response).join();

        if (response.statusCode >= 200 && response.statusCode < 300) {
          final decoded = jsonDecode(body);
          List<dynamic> items = [];
          if (decoded is List) {
            items = decoded;
          } else if (decoded is Map) {
            items = decoded['rules'] ?? decoded['items'] ?? decoded['data'] ?? [];
          }
          final rules = items
              .whereType<Map<String, dynamic>>()
              .map(ProxyRule.fromJson)
              .toList();

          if (mounted) {
            setState(() {
              _rules = rules;
              _loading = false;
            });
          }
        } else {
          throw HttpException('HTTP ${response.statusCode}');
        }
      } finally {
        client.close();
      }
    } catch (e) {
      if (mounted) {
        setState(() {
          _error = 'Failed to fetch rules: $e';
          _loading = false;
        });
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final l10n = AppLocalizations.of(context)!;
    final isRegistered = widget.state.profile.isRegistered;

    return Scaffold(
      appBar: AppBar(
        title: Text(l10n.titleRules),
        actions: [
          if (isRegistered)
            IconButton(
              onPressed: _loading ? null : _fetchRules,
              icon: _loading
                  ? const SizedBox.square(
                      dimension: 20,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.refresh),
            ),
        ],
      ),
      body: !isRegistered
          ? _buildNotRegistered(theme, l10n)
          : _error != null
              ? _buildError(theme, l10n)
              : _rules.isEmpty && !_loading
                  ? _buildEmpty(theme, l10n)
                  : _buildRulesList(theme, l10n),
    );
  }

  Widget _buildNotRegistered(ThemeData theme, AppLocalizations l10n) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(Icons.cloud_off, size: 48, color: theme.colorScheme.outline),
            const SizedBox(height: 16),
            Text(
              l10n.titleNotConnected,
              style: theme.textTheme.titleMedium,
            ),
            const SizedBox(height: 8),
            Text(
              l10n.descNotConnected,
              textAlign: TextAlign.center,
              style: theme.textTheme.bodyMedium?.copyWith(color: theme.colorScheme.outline),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildError(ThemeData theme, AppLocalizations l10n) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(Icons.error, size: 48, color: theme.colorScheme.error),
            const SizedBox(height: 16),
            Text(l10n.titleError, style: theme.textTheme.titleMedium),
            const SizedBox(height: 8),
            Text(
              _error!,
              textAlign: TextAlign.center,
              style: TextStyle(color: theme.colorScheme.error),
            ),
            const SizedBox(height: 16),
            FilledButton(onPressed: _fetchRules, child: Text(l10n.btnRetry)),
          ],
        ),
      ),
    );
  }

  Widget _buildEmpty(ThemeData theme, AppLocalizations l10n) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Icon(Icons.format_list_bulleted, size: 48, color: theme.colorScheme.outline),
            const SizedBox(height: 16),
            Text(l10n.titleNoRules, style: theme.textTheme.titleMedium),
            const SizedBox(height: 8),
            Text(
              l10n.descNoRules,
              textAlign: TextAlign.center,
              style: theme.textTheme.bodyMedium?.copyWith(color: theme.colorScheme.outline),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildRulesList(ThemeData theme, AppLocalizations l10n) {
    return ListView.separated(
      padding: const EdgeInsets.all(16),
      itemCount: _rules.length,
      separatorBuilder: (_, _) => const SizedBox(height: 8),
      itemBuilder: (context, index) {
        final rule = _rules[index];
        return Card(
          child: ListTile(
            leading: Icon(
              rule.enabled ? Icons.check_circle : Icons.cancel,
              color: rule.enabled ? Colors.green : theme.colorScheme.error,
            ),
            title: Text(rule.domain),
            subtitle: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text('${l10n.labelTarget}: ${rule.target}'),
                Text('${l10n.labelType}: ${rule.type.toUpperCase()}'),
              ],
            ),
            isThreeLine: true,
            trailing: Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
              decoration: BoxDecoration(
                color: rule.enabled
                    ? Colors.green.withValues(alpha: 0.1)
                    : theme.colorScheme.errorContainer,
                borderRadius: BorderRadius.circular(8),
              ),
              child: Text(
                rule.enabled ? l10n.labelEnabled : l10n.labelDisabled,
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  color: rule.enabled ? Colors.green : theme.colorScheme.error,
                ),
              ),
            ),
          ),
        );
      },
    );
  }
}
