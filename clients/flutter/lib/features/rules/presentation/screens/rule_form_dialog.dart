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
  ProxyRule? existingRule,
}) {
  return showDialog<bool>(
    context: context,
    barrierColor: Colors.black.withValues(alpha: 0.5),
    builder: (context) => _RuleFormDialog(
      mode: mode,
      existingRule: existingRule,
    ),
  );
}

// ---------------------------------------------------------------------------
// Dialog implementation
// ---------------------------------------------------------------------------

class _RuleFormDialog extends ConsumerStatefulWidget {
  const _RuleFormDialog({
    required this.mode,
    this.existingRule,
  });

  final RuleFormMode mode;
  final ProxyRule? existingRule;

  @override
  ConsumerState<_RuleFormDialog> createState() => _RuleFormDialogState();
}

class _RuleFormDialogState extends ConsumerState<_RuleFormDialog> {
  late final TextEditingController _domainController;
  late final TextEditingController _targetController;
  String _selectedType = 'http';
  bool _saving = false;

  static const _typeOptions = ['http', 'https', 'l4'];

  @override
  void initState() {
    super.initState();
    if (widget.mode == RuleFormMode.edit && widget.existingRule != null) {
      _domainController =
          TextEditingController(text: widget.existingRule!.domain);
      _targetController =
          TextEditingController(text: widget.existingRule!.target);
      _selectedType = widget.existingRule!.type.toLowerCase();
    } else {
      _domainController = TextEditingController();
      _targetController = TextEditingController();
    }
  }

  @override
  void dispose() {
    _domainController.dispose();
    _targetController.dispose();
    super.dispose();
  }

  bool get _isValid {
    final domain = _domainController.text.trim();
    final target = _targetController.text.trim();
    return domain.isNotEmpty && target.isNotEmpty;
  }

  Future<void> _save() async {
    if (!_isValid || _saving) return;

    setState(() => _saving = true);

    final domain = _domainController.text.trim();
    final target = _targetController.text.trim();

    try {
      if (widget.mode == RuleFormMode.create) {
        final request = CreateRuleRequest(
          domain: domain,
          target: target,
          type: _selectedType,
        );
        await ref.read(rulesListProvider.notifier).createRule(request);
      } else {
        final request = UpdateRuleRequest(
          domain: domain,
          target: target,
          type: _selectedType,
        );
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
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                // -- Header ----
                Text(
                  isEdit ? loc.titleEditRule : loc.titleNewRule,
                  style: AppTypography.title.copyWith(
                    color: AppColors.textPrimary,
                    fontSize: 16,
                  ),
                ),
                const SizedBox(height: AppSpacing.s20),

                // -- Domain field ----
                _FieldLabel(loc.labelDomain),
                const SizedBox(height: AppSpacing.s4),
                _GlassTextField(
                  controller: _domainController,
                  hint: 'example.com',
                  onChanged: (_) => setState(() {}),
                ),
                const SizedBox(height: AppSpacing.s14),

                // -- Target field ----
                _FieldLabel(loc.labelTarget),
                const SizedBox(height: AppSpacing.s4),
                _GlassTextField(
                  controller: _targetController,
                  hint: 'localhost:8080',
                  onChanged: (_) => setState(() {}),
                ),
                const SizedBox(height: AppSpacing.s14),

                // -- Type dropdown ----
                _FieldLabel(loc.labelType),
                const SizedBox(height: AppSpacing.s4),
                _GlassTypeDropdown(
                  value: _selectedType,
                  items: _typeOptions,
                  onChanged: (value) {
                    if (value != null) {
                      setState(() => _selectedType = value);
                    }
                  },
                ),
                const SizedBox(height: AppSpacing.s20),

                // -- Actions ----
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
        filter: ImageFilter.blur(sigmaX: AppBlur.subtle, sigmaY: AppBlur.subtle),
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

class _GlassTypeDropdown extends StatelessWidget {
  const _GlassTypeDropdown({
    required this.value,
    required this.items,
    required this.onChanged,
  });

  final String value;
  final List<String> items;
  final ValueChanged<String?> onChanged;

  @override
  Widget build(BuildContext context) {
    return ClipRRect(
      borderRadius: BorderRadius.circular(AppRadius.medium),
      child: BackdropFilter(
        filter: ImageFilter.blur(sigmaX: AppBlur.subtle, sigmaY: AppBlur.subtle),
        child: Container(
          padding: const EdgeInsets.symmetric(
            horizontal: AppSpacing.s12,
          ),
          decoration: BoxDecoration(
            color: Colors.white.withValues(alpha: AppColors.surfaceOpacityCard),
            borderRadius: BorderRadius.circular(AppRadius.medium),
            border: Border.all(color: AppColors.border),
          ),
          child: DropdownButtonHideUnderline(
            child: DropdownButton<String>(
              value: value,
              items: items
                  .map(
                    (item) => DropdownMenuItem(
                      value: item,
                      child: Text(
                        item.toUpperCase(),
                        style: AppTypography.body.copyWith(
                          color: AppColors.textPrimary,
                          fontWeight: FontWeight.w500,
                        ),
                      ),
                    ),
                  )
                  .toList(),
              onChanged: onChanged,
              dropdownColor: const Color(0xFF1E293B),
              iconEnabledColor: AppColors.textMuted,
              style: AppTypography.body.copyWith(color: AppColors.textPrimary),
              isDense: true,
              isExpanded: true,
            ),
          ),
        ),
      ),
    );
  }
}
