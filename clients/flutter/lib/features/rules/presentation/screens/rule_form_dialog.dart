import 'dart:ui';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../../../../core/design/components/glass_button.dart';
import '../../../../core/design/tokens/app_colors.dart';
import '../../../../core/design/tokens/app_spacing.dart';
import '../../../../core/design/tokens/app_typography.dart';
import '../../../../l10n/app_localizations.dart';
import '../../data/models/rule_models.dart';
import '../providers/rules_provider.dart';

/// Dialog mode -- create or edit.
enum RuleFormMode { create, edit }

/// A glassmorphism-styled dialog for creating or editing a proxy rule.
///
/// Returns `true` when confirmed (the provider has already been updated).
/// Returns `null` when cancelled.
Future<bool?> showRuleFormDialog(
  BuildContext context, {
  RuleFormMode mode = RuleFormMode.create,
  HttpProxyRule? existingRule,
}) {
  return showDialog<bool>(
    context: context,
    barrierColor: Colors.black.withValues(alpha: 0.5),
    builder: (context) =>
        _RuleFormDialog(mode: mode, existingRule: existingRule),
  );
}

// ---------------------------------------------------------------------------
// Dialog implementation
// ---------------------------------------------------------------------------

class _RuleFormDialog extends ConsumerStatefulWidget {
  const _RuleFormDialog({required this.mode, this.existingRule});

  final RuleFormMode mode;
  final HttpProxyRule? existingRule;

  @override
  ConsumerState<_RuleFormDialog> createState() => _RuleFormDialogState();
}

class _RuleFormDialogState extends ConsumerState<_RuleFormDialog> {
  late final TextEditingController _frontendController;
  late final TextEditingController _backendController;
  late final TextEditingController _tagsController;
  late final TextEditingController _userAgentController;
  late bool _enabled;
  late bool _proxyRedirect;
  late bool _passProxyHeaders;
  bool _saving = false;

  @override
  void initState() {
    super.initState();
    if (widget.mode == RuleFormMode.edit && widget.existingRule != null) {
      final rule = widget.existingRule!;
      _frontendController = TextEditingController(
        text: widget.existingRule!.frontendUrl,
      );
      _backendController = TextEditingController(text: rule.backendUrl);
      _tagsController = TextEditingController(text: rule.tags.join(', '));
      _userAgentController = TextEditingController(text: rule.userAgent ?? '');
      _enabled = rule.enabled;
      _proxyRedirect = rule.proxyRedirect ?? false;
      _passProxyHeaders = rule.passProxyHeaders ?? true;
    } else {
      _frontendController = TextEditingController();
      _backendController = TextEditingController();
      _tagsController = TextEditingController();
      _userAgentController = TextEditingController();
      _enabled = true;
      _proxyRedirect = false;
      _passProxyHeaders = true;
    }
  }

  @override
  void dispose() {
    _frontendController.dispose();
    _backendController.dispose();
    _tagsController.dispose();
    _userAgentController.dispose();
    super.dispose();
  }

  bool get _isValid {
    final frontend = _frontendController.text.trim();
    final backend = _backendController.text.trim();
    return frontend.isNotEmpty && backend.isNotEmpty;
  }

  Future<void> _save() async {
    if (!_isValid || _saving) return;

    setState(() => _saving = true);

    final frontendUrl = _frontendController.text.trim();
    final backendUrl = _backendController.text.trim();
    final tags = _tagsController.text
        .split(',')
        .map((tag) => tag.trim())
        .where((tag) => tag.isNotEmpty)
        .toList();
    final userAgent = _userAgentController.text.trim();
    final editedRule = HttpProxyRule(
      id: widget.existingRule?.id ?? '',
      frontendUrl: frontendUrl,
      backends: [HttpBackend(url: backendUrl)],
      enabled: _enabled,
      tags: tags,
      proxyRedirect: _proxyRedirect,
      passProxyHeaders: _passProxyHeaders,
      userAgent: userAgent.isEmpty ? null : userAgent,
      customHeaders: widget.existingRule?.customHeaders ?? const [],
      loadBalancingStrategy: widget.existingRule?.loadBalancingStrategy,
      relayLayers: widget.existingRule?.relayLayers ?? const [],
      relayObfs: widget.existingRule?.relayObfs,
    );

    try {
      if (widget.mode == RuleFormMode.create) {
        final request = CreateHttpRuleRequest(
          frontendUrl: frontendUrl,
          backends: [HttpBackend(url: backendUrl)],
          enabled: _enabled,
          tags: tags,
          proxyRedirect: _proxyRedirect,
          passProxyHeaders: _passProxyHeaders,
          userAgent: userAgent.isEmpty ? null : userAgent,
        );
        await ref.read(rulesListProvider.notifier).createRule(request);
      } else {
        final request = UpdateHttpRuleRequest.fromRule(editedRule);
        await ref
            .read(rulesListProvider.notifier)
            .updateRule(widget.existingRule!.id, request);
      }

      if (mounted) {
        Navigator.of(context).pop(true);
      }
    } catch (e) {
      if (mounted) {
        final loc = AppLocalizations.of(context)!;
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(
            content: Text(loc.msgFailedToSaveRule('$e')),
            backgroundColor: AppColors.error,
          ),
        );
      }
    } finally {
      if (mounted) {
        setState(() => _saving = false);
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final isEdit = widget.mode == RuleFormMode.edit;
    final screenWidth = MediaQuery.sizeOf(context).width;
    final loc = AppLocalizations.of(context)!;
    final dialogWidth = screenWidth > 500 ? 460.0 : screenWidth - 32.0;

    return Center(
      child: ClipRRect(
        borderRadius: BorderRadius.circular(AppRadius.largeCard),
        child: BackdropFilter(
          filter: ImageFilter.blur(
            sigmaX: AppBlur.heavy,
            sigmaY: AppBlur.heavy,
          ),
          child: Container(
            width: dialogWidth,
            padding: const EdgeInsets.all(AppSpacing.s20),
            decoration: BoxDecoration(
              color: const Color(0xFF1E293B).withValues(alpha: 0.95),
              borderRadius: BorderRadius.circular(AppRadius.largeCard),
              border: Border.all(color: AppColors.border),
            ),
            child: Material(
              color: Colors.transparent,
              child: SingleChildScrollView(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Text(
                      isEdit ? loc.titleEditRule : loc.titleNewRule,
                      style: AppTypography.title.copyWith(
                        color: AppColors.textPrimary,
                        fontSize: 16,
                      ),
                    ),
                    const SizedBox(height: AppSpacing.s20),
                    const _FieldLabel('Frontend URL'),
                    const SizedBox(height: AppSpacing.s4),
                    _GlassTextField(
                      controller: _frontendController,
                      hint: 'https://emby.example.com',
                      onChanged: (_) => setState(() {}),
                    ),
                    const SizedBox(height: AppSpacing.s14),
                    const _FieldLabel('Backend URL'),
                    const SizedBox(height: AppSpacing.s4),
                    _GlassTextField(
                      controller: _backendController,
                      hint: 'http://emby:8096',
                      onChanged: (_) => setState(() {}),
                    ),
                    const SizedBox(height: AppSpacing.s14),
                    _SwitchRow(
                      label: 'Enabled',
                      value: _enabled,
                      onChanged: (value) => setState(() => _enabled = value),
                    ),
                    const SizedBox(height: AppSpacing.s14),
                    const _FieldLabel('Tags'),
                    const SizedBox(height: AppSpacing.s4),
                    _GlassTextField(
                      controller: _tagsController,
                      hint: 'media, edge',
                    ),
                    const SizedBox(height: AppSpacing.s14),
                    _SwitchRow(
                      label: 'Proxy redirect',
                      value: _proxyRedirect,
                      onChanged: (value) =>
                          setState(() => _proxyRedirect = value),
                    ),
                    const SizedBox(height: AppSpacing.s8),
                    _SwitchRow(
                      label: 'Pass proxy headers',
                      value: _passProxyHeaders,
                      onChanged: (value) =>
                          setState(() => _passProxyHeaders = value),
                    ),
                    const SizedBox(height: AppSpacing.s14),
                    const _FieldLabel('User agent'),
                    const SizedBox(height: AppSpacing.s4),
                    _GlassTextField(
                      controller: _userAgentController,
                      hint: 'Optional',
                    ),
                    const SizedBox(height: AppSpacing.s20),
                    Row(
                      mainAxisAlignment: MainAxisAlignment.end,
                      children: [
                        GlassButton.secondary(
                          label: loc.btnCancel,
                          onPressed: _saving
                              ? null
                              : () => Navigator.of(context).pop(null),
                        ),
                        const SizedBox(width: AppSpacing.s8),
                        GlassButton.primary(
                          label: _saving ? loc.btnSaving : loc.btnSave,
                          onPressed: _isValid && !_saving ? _save : null,
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
    );
  }
}

// ---------------------------------------------------------------------------
// Supporting widgets
// ---------------------------------------------------------------------------

class _FieldLabel extends StatelessWidget {
  const _FieldLabel(this.label);
  final String label;

  @override
  Widget build(BuildContext context) {
    return Text(
      label,
      style: AppTypography.metadata.copyWith(
        fontWeight: FontWeight.w500,
        color: AppColors.textSecondary,
      ),
    );
  }
}

class _GlassTextField extends StatelessWidget {
  const _GlassTextField({
    required this.controller,
    required this.hint,
    this.onChanged,
  });

  final TextEditingController controller;
  final String hint;
  final ValueChanged<String>? onChanged;

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(AppRadius.medium),
      child: BackdropFilter(
        filter: ImageFilter.blur(
          sigmaX: AppBlur.subtle,
          sigmaY: AppBlur.subtle,
        ),
        child: Container(
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: AppColors.surfaceOpacityCard),
            borderRadius: BorderRadius.circular(AppRadius.medium),
            border: Border.all(color: AppColors.border),
          ),
          child: TextField(
            controller: controller,
            onChanged: onChanged,
            style: AppTypography.body.copyWith(color: AppColors.textPrimary),
            decoration: InputDecoration(
              hintText: hint,
              hintStyle: AppTypography.body.copyWith(
                color: AppColors.textMuted,
              ),
              border: InputBorder.none,
              enabledBorder: InputBorder.none,
              focusedBorder: InputBorder.none,
              contentPadding: const EdgeInsets.symmetric(
                horizontal: AppSpacing.s12,
                vertical: AppSpacing.s10,
              ),
              isDense: true,
            ),
          ),
        ),
      ),
    );
  }
}

class _SwitchRow extends StatelessWidget {
  const _SwitchRow({
    required this.label,
    required this.value,
    required this.onChanged,
  });

  final String label;
  final bool value;
  final ValueChanged<bool> onChanged;

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(AppRadius.medium),
      child: BackdropFilter(
        filter: ImageFilter.blur(
          sigmaX: AppBlur.subtle,
          sigmaY: AppBlur.subtle,
        ),
        child: Container(
          padding: const EdgeInsets.symmetric(
            horizontal: AppSpacing.s12,
            vertical: AppSpacing.s4,
          ),
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: AppColors.surfaceOpacityCard),
            borderRadius: BorderRadius.circular(AppRadius.medium),
            border: Border.all(color: AppColors.border),
          ),
          child: Row(
            children: [
              Expanded(
                child: Text(
                  label,
                  style: AppTypography.body.copyWith(
                    color: AppColors.textPrimary,
                  ),
                ),
              ),
              Switch(
                value: value,
                onChanged: onChanged,
                activeThumbColor: AppColors.info,
                inactiveThumbColor: AppColors.textMuted,
                inactiveTrackColor: AppColors.textMuted.withValues(alpha: 0.2),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
