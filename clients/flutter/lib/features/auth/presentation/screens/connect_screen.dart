import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../../../../core/design/components/glass_button.dart';
import '../../../../core/design/components/glass_card.dart';
import '../../../../core/design/theme/accent_themes.dart';
import '../../../../core/design/theme/theme_controller.dart';
import '../../../../core/design/tokens/app_colors.dart';
import '../../../../core/design/tokens/app_spacing.dart';
import '../../../../core/routing/route_names.dart';
import '../../../../l10n/app_localizations.dart';
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
  var _mode = ConnectionMode.management;
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
    final accent = ref.watch(
      themeControllerProvider.select(
        (s) => s.value?.accent ?? AccentThemes.defaults,
      ),
    );
    final authAsync = ref.watch(authNotifierProvider);
    final loc = AppLocalizations.of(context)!;

    final stepLabels = [
      loc.stepServerUrl,
      loc.stepRegisterToken,
      loc.stepClientName,
    ];

    ref.listen(authNotifierProvider, (_, next) {
      if (next.value is AuthStateAuthenticated) {
        context.go(RouteNames.dashboard);
      }
    });

    return Scaffold(
      backgroundColor: Colors.transparent,
      body: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 400),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: AppSpacing.s20),
            child: GlassCard(
              blur: AppBlur.heavy,
              borderRadius: AppRadius.largeCard,
              padding: const EdgeInsets.all(AppSpacing.s20),
              child: Form(
                key: _formKey,
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    // -- Logo ---------------------------------------------------
                    Center(
                      child: Container(
                        width: 48,
                        height: 48,
                        decoration: BoxDecoration(
                          gradient: accent.primaryGradient,
                          borderRadius: BorderRadius.circular(12),
                        ),
                        alignment: Alignment.center,
                        child: const Text(
                          'N',
                          style: TextStyle(
                            color: AppColors.textPrimary,
                            fontSize: 24,
                            fontWeight: FontWeight.w700,
                            height: 1.0,
                          ),
                        ),
                      ),
                    ),
                    const SizedBox(height: AppSpacing.s16),

                    // -- Title --------------------------------------------------
                    Center(
                      child: Text(
                        loc.titleConnectToMaster,
                        style: const TextStyle(
                          fontSize: 18,
                          fontWeight: FontWeight.w700,
                          color: AppColors.textPrimary,
                          height: 1.4,
                        ),
                      ),
                    ),
                    const SizedBox(height: AppSpacing.s8),

                    // -- Mode selector ------------------------------------------
                    Center(
                      child: SegmentedButton<ConnectionMode>(
                        segments: const [
                          ButtonSegment(
                            value: ConnectionMode.management,
                            label: Text('Management'),
                          ),
                          ButtonSegment(
                            value: ConnectionMode.agent,
                            label: Text('Agent'),
                          ),
                        ],
                        selected: {_mode},
                        onSelectionChanged: (selected) {
                          setState(() {
                            _mode = selected.single;
                            _step = 0;
                          });
                        },
                      ),
                    ),
                    const SizedBox(height: AppSpacing.s20),

                    if (_mode == ConnectionMode.agent) ...[
                      // -- Step indicator ---------------------------------------
                      Center(
                        child: _StepIndicator(
                          currentStep: _step,
                          totalSteps: 3,
                          accent: accent,
                        ),
                      ),
                      const SizedBox(height: AppSpacing.s4),
                      Center(
                        child: Text(
                          stepLabels[_step],
                          style: const TextStyle(
                            fontSize: 11,
                            fontWeight: FontWeight.w500,
                            color: AppColors.textMuted,
                          ),
                        ),
                      ),
                      const SizedBox(height: AppSpacing.s20),
                    ],

                    // -- Step content -------------------------------------------
                    _buildStepContent(loc),

                    // -- Error state --------------------------------------------
                    if (authAsync.value is AuthStateError) ...[
                      const SizedBox(height: AppSpacing.s12),
                      Padding(
                        padding: const EdgeInsets.symmetric(
                          horizontal: AppSpacing.s4,
                        ),
                        child: Text(
                          (authAsync.value as AuthStateError).message,
                          style: const TextStyle(
                            fontSize: 12,
                            color: AppColors.error,
                          ),
                        ),
                      ),
                    ],

                    const SizedBox(height: AppSpacing.s20),

                    // -- Navigation buttons -------------------------------------
                    _buildNavButtons(authAsync, accent, loc),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildStepContent(AppLocalizations loc) {
    if (_mode == ConnectionMode.management) {
      return Column(
        children: [
          TextFormField(
            controller: _urlController,
            decoration: InputDecoration(
              labelText: loc.labelMasterUrl,
              hintText: loc.hintMasterUrl,
              prefixIcon: const Icon(Icons.link, size: 18),
            ),
            validator: (v) => v == null || v.isEmpty ? loc.errorEnterUrl : null,
          ),
          const SizedBox(height: AppSpacing.s12),
          TextFormField(
            controller: _tokenController,
            decoration: InputDecoration(
              labelText: loc.labelPanelToken,
              prefixIcon: const Icon(Icons.key, size: 18),
            ),
            obscureText: true,
            validator: (v) =>
                v == null || v.isEmpty ? loc.errorEnterToken : null,
          ),
          const SizedBox(height: AppSpacing.s12),
          TextFormField(
            controller: _nameController,
            decoration: InputDecoration(
              labelText: loc.labelClientName,
              hintText: loc.hintClientName,
              prefixIcon: const Icon(Icons.badge, size: 18),
            ),
          ),
        ],
      );
    }

    return AnimatedSwitcher(
      duration: const Duration(milliseconds: 200),
      child: KeyedSubtree(
        key: ValueKey(_step),
        child: Column(
          children: [
            if (_step == 0)
              TextFormField(
                controller: _urlController,
                decoration: InputDecoration(
                  labelText: loc.labelMasterUrl,
                  hintText: loc.hintMasterUrl,
                  prefixIcon: const Icon(Icons.link, size: 18),
                ),
                validator: (v) =>
                    v == null || v.isEmpty ? loc.errorEnterUrl : null,
              )
            else if (_step == 1)
              TextFormField(
                controller: _tokenController,
                decoration: InputDecoration(
                  labelText: loc.labelRegisterToken,
                  hintText: loc.hintRegisterToken,
                  prefixIcon: const Icon(Icons.key, size: 18),
                ),
                obscureText: true,
                validator: (v) =>
                    v == null || v.isEmpty ? loc.errorEnterToken : null,
              )
            else
              TextFormField(
                controller: _nameController,
                decoration: InputDecoration(
                  labelText: loc.labelClientName,
                  hintText: loc.hintClientName,
                  prefixIcon: const Icon(Icons.badge, size: 18),
                ),
              ),
          ],
        ),
      ),
    );
  }

  Widget _buildNavButtons(
    AsyncValue<AuthState> authAsync,
    AccentColors accent,
    AppLocalizations loc,
  ) {
    final isLoading = authAsync.value is AuthStateLoading;

    return Row(
      children: [
        if (_mode == ConnectionMode.agent && _step > 0) ...[
          GlassButton.secondary(
            label: loc.btnPrevious,
            onPressed: isLoading ? null : () => setState(() => _step--),
          ),
          const Spacer(),
        ] else
          const Spacer(),
        GlassButton.primary(
          label: _mode == ConnectionMode.agent && _step < 2
              ? loc.btnNext
              : loc.btnConnect,
          onPressed: isLoading ? null : _onNext,
          accentStart: accent.primaryStart,
          accentEnd: accent.primaryEnd,
        ),
      ],
    );
  }

  void _onNext() {
    if (!(_formKey.currentState?.validate() ?? false)) return;
    if (_mode == ConnectionMode.management) {
      ref
          .read(authNotifierProvider.notifier)
          .connectManagement(
            masterUrl: _urlController.text.trim(),
            panelToken: _tokenController.text.trim(),
            name: _nameController.text.trim(),
          );
      return;
    }

    if (_step < 2) {
      setState(() => _step++);
    } else {
      ref
          .read(authNotifierProvider.notifier)
          .register(
            masterUrl: _urlController.text.trim(),
            registerToken: _tokenController.text.trim(),
            name: _nameController.text.trim(),
          );
    }
  }
}

// ---------------------------------------------------------------------------
// Step indicator dots
// ---------------------------------------------------------------------------

class _StepIndicator extends StatelessWidget {
  const _StepIndicator({
    required this.currentStep,
    required this.totalSteps,
    required this.accent,
  });

  final int currentStep;
  final int totalSteps;
  final AccentColors accent;

  @override
  Widget build(BuildContext context) {
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: List.generate(totalSteps, (index) {
        final isActive = index == currentStep;
        final isCompleted = index < currentStep;
        return Padding(
          padding: EdgeInsets.only(
            right: index < totalSteps - 1 ? AppSpacing.s8 : 0,
          ),
          child: AnimatedContainer(
            duration: const Duration(milliseconds: 200),
            width: isActive ? 20 : 8,
            height: 8,
            decoration: BoxDecoration(
              color: isActive
                  ? accent.primaryStart
                  : isCompleted
                  ? accent.primaryStart.withValues(alpha: 0.4)
                  : AppColors.textMuted.withValues(alpha: 0.3),
              borderRadius: BorderRadius.circular(4),
            ),
          ),
        );
      }),
    );
  }
}
