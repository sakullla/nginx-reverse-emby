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
          ? _EmptyState(
              icon: Icons.cloud_off,
              iconColor: theme.colorScheme.outline,
              title: l10n.titleNotConnected,
              message: l10n.descNotConnected,
            )
          : _error != null
              ? _EmptyState(
                  icon: Icons.error_outline,
                  iconColor: theme.colorScheme.error,
                  title: l10n.titleError,
                  message: _error!,
                  isError: true,
                  action: FilledButton(
                    onPressed: _fetchRules,
                    child: Text(l10n.btnRetry),
                  ),
                )
              : _rules.isEmpty && !_loading
                  ? _EmptyState(
                      icon: Icons.format_list_bulleted,
                      iconColor: theme.colorScheme.outline,
                      title: l10n.titleNoRules,
                      message: l10n.descNoRules,
                    )
                  : _buildRulesList(theme, l10n),
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
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
            child: Row(
              children: [
                Icon(
                  rule.enabled ? Icons.check_circle : Icons.cancel,
                  color: rule.enabled ? Colors.green : theme.colorScheme.error,
                  size: 22,
                ),
                const SizedBox(width: 14),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        rule.domain,
                        style: theme.textTheme.titleSmall?.copyWith(fontWeight: FontWeight.w600),
                      ),
                      const SizedBox(height: 4),
                      Row(
                        children: [
                          _Tag(text: '${l10n.labelTarget}: ${rule.target}'),
                          const SizedBox(width: 8),
                          _Tag(text: rule.type.toUpperCase()),
                        ],
                      ),
                    ],
                  ),
                ),
                Container(
                  padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
                  decoration: BoxDecoration(
                    color: rule.enabled
                        ? Colors.green.withValues(alpha: 0.1)
                        : theme.colorScheme.errorContainer,
                    borderRadius: BorderRadius.circular(20),
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
              ],
            ),
          ),
        );
      },
    );
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState({
    required this.icon,
    required this.iconColor,
    required this.title,
    required this.message,
    this.isError = false,
    this.action,
  });

  final IconData icon;
  final Color iconColor;
  final String title;
  final String message;
  final bool isError;
  final Widget? action;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: Padding(
        padding: const EdgeInsets.all(32),
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 360),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Container(
                padding: const EdgeInsets.all(20),
                decoration: BoxDecoration(
                  color: iconColor.withValues(alpha: 0.08),
                  shape: BoxShape.circle,
                ),
                child: Icon(icon, size: 40, color: iconColor),
              ),
              const SizedBox(height: 20),
              Text(
                title,
                style: theme.textTheme.titleMedium?.copyWith(fontWeight: FontWeight.bold),
              ),
              const SizedBox(height: 8),
              Text(
                message,
                textAlign: TextAlign.center,
                style: theme.textTheme.bodyMedium?.copyWith(
                  color: isError ? theme.colorScheme.error : theme.colorScheme.outline,
                ),
              ),
              if (action != null) ...[
                const SizedBox(height: 20),
                action!,
              ],
            ],
          ),
        ),
      ),
    );
  }
}

class _Tag extends StatelessWidget {
  const _Tag({required this.text});

  final String text;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: theme.colorScheme.surfaceContainerHighest,
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        text,
        style: theme.textTheme.bodySmall?.copyWith(
          color: theme.colorScheme.onSurfaceVariant,
          fontWeight: FontWeight.w500,
        ),
      ),
    );
  }
}
