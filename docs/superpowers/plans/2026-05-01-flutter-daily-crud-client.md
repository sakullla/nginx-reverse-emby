# Flutter Daily CRUD Client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the first complete Flutter daily-operations client milestone with management-mode CRUD and preserved local agent controls.

**Architecture:** Extend the existing Flutter app rather than replacing it. Add a typed panel API layer using panel-token auth, extend the current profile/auth model to hold both management and agent credentials, then wire Riverpod feature stores and existing screens to typed models/actions.

**Tech Stack:** Flutter 3/Dart, Riverpod annotations and generated providers, Dio, SharedPreferences, flutter_test, mocktail, existing glassmorphism component library.

---

## File Structure

### Auth and Profile

- Modify `clients/flutter/lib/features/auth/data/models/auth_models.dart`
  - Add `ConnectionMode`, `ManagementProfile`, `AgentProfile`, and expand `ClientProfile`.
  - Keep backwards-compatible parsing for existing saved `agentId/token` profiles.
- Modify `clients/flutter/lib/features/auth/data/repositories/auth_repository.dart`
  - Continue storing one JSON profile under `client_profile`.
- Modify `clients/flutter/lib/features/auth/presentation/providers/auth_provider.dart`
  - Add `connectManagement`, keep register-token agent registration, add profile-clear methods.
- Modify `clients/flutter/lib/features/auth/presentation/screens/connect_screen.dart`
  - Add management/agent mode selection and panel token fields.
- Test `clients/flutter/test/features/auth/auth_models_test.dart`
- Test `clients/flutter/test/features/auth/auth_provider_test.dart`
- Test `clients/flutter/test/features/auth/connect_screen_test.dart`

### Network and Models

- Create `clients/flutter/lib/core/network/panel_api_client.dart`
  - Typed management API for `/panel-api/*`.
  - Owns response envelope parsing and long-running options.
- Modify `clients/flutter/lib/core/network/dio_client.dart`
  - Support `X-Panel-Token` header and preserve existing bearer mode only if still needed by tests.
- Modify `clients/flutter/lib/core/network/api_client.dart`
  - Replace the thin generic API surface or mark it as a compatibility shim backed by `PanelApiClient`.
- Modify `clients/flutter/lib/core/network/master_api.dart`
  - Either delegate to `PanelApiClient` or remove provider usage after callers migrate.
- Modify `clients/flutter/lib/features/rules/data/models/rule_models.dart`
- Create `clients/flutter/lib/features/agents/data/models/agent_models.dart`
- Modify `clients/flutter/lib/features/certificates/data/models/certificate_models.dart`
- Modify `clients/flutter/lib/features/relay/data/models/relay_models.dart`
- Test `clients/flutter/test/core/network/panel_api_client_test.dart`
- Test `clients/flutter/test/features/rules/rule_models_test.dart`
- Test `clients/flutter/test/features/agents/agent_models_test.dart`
- Test `clients/flutter/test/features/certificates/certificate_models_test.dart`
- Test `clients/flutter/test/features/relay/relay_models_test.dart`

### Providers

- Create `clients/flutter/lib/features/agents/presentation/providers/agents_provider.dart`
- Modify `clients/flutter/lib/features/rules/presentation/providers/rules_provider.dart`
- Modify `clients/flutter/lib/features/certificates/presentation/providers/certificates_provider.dart`
- Modify `clients/flutter/lib/features/relay/presentation/providers/relay_provider.dart`
- Create `clients/flutter/lib/features/dashboard/presentation/providers/dashboard_provider.dart`
- Regenerate `*.g.dart` files with `dart run build_runner build --delete-conflicting-outputs`.
- Test provider behavior under `clients/flutter/test/features/**`.

### Screens

- Modify `clients/flutter/lib/features/dashboard/presentation/screens/dashboard_screen.dart`
- Modify `clients/flutter/lib/features/rules/presentation/screens/rules_list_screen.dart`
- Modify `clients/flutter/lib/features/rules/presentation/screens/rule_form_dialog.dart`
- Modify `clients/flutter/lib/features/agents/presentation/screens/agents_screen.dart`
- Modify `clients/flutter/lib/features/certificates/presentation/screens/certificates_screen.dart`
- Modify `clients/flutter/lib/features/relay/presentation/screens/relay_screen.dart`
- Modify `clients/flutter/lib/features/settings/presentation/screens/settings_screen.dart`
- Keep shell routes unchanged; implement new workflows with existing pages and dialogs.

---

## Task 1: Auth Profile Supports Management and Agent Modes

**Files:**
- Modify: `clients/flutter/lib/features/auth/data/models/auth_models.dart`
- Modify: `clients/flutter/lib/features/auth/data/repositories/auth_repository.dart`
- Modify: `clients/flutter/lib/features/auth/presentation/providers/auth_provider.dart`
- Test: `clients/flutter/test/features/auth/auth_models_test.dart`
- Test: `clients/flutter/test/features/auth/auth_provider_test.dart`

- [ ] **Step 1: Write failing auth model tests**

Create `clients/flutter/test/features/auth/auth_models_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/features/auth/data/models/auth_models.dart';

void main() {
  test('ClientProfile stores management and agent credentials independently', () {
    final profile = ClientProfile(
      masterUrl: 'https://panel.example.com',
      displayName: 'ops-laptop',
      activeMode: ConnectionMode.management,
      management: const ManagementProfile(panelToken: 'panel-secret'),
      agent: const AgentProfile(agentId: 'agent-1', agentToken: 'agent-secret'),
    );

    expect(profile.hasManagementCredentials, isTrue);
    expect(profile.hasAgentCredentials, isTrue);
    expect(profile.isRegistered, isTrue);
    expect(profile.management.panelToken, 'panel-secret');
    expect(profile.agent.agentToken, 'agent-secret');
  });

  test('ClientProfile parses legacy agentId token profile', () {
    final profile = ClientProfile.fromJson({
      'masterUrl': 'https://panel.example.com',
      'displayName': 'legacy-client',
      'agentId': 'agent-legacy',
      'token': 'legacy-token',
    });

    expect(profile.activeMode, ConnectionMode.agent);
    expect(profile.agent.agentId, 'agent-legacy');
    expect(profile.agent.agentToken, 'legacy-token');
    expect(profile.hasAgentCredentials, isTrue);
    expect(profile.hasManagementCredentials, isFalse);
  });

  test('clearManagement leaves agent credentials intact', () {
    final profile = ClientProfile(
      masterUrl: 'https://panel.example.com',
      activeMode: ConnectionMode.management,
      management: const ManagementProfile(panelToken: 'panel-secret'),
      agent: const AgentProfile(agentId: 'agent-1', agentToken: 'agent-secret'),
    );

    final cleared = profile.clearManagement();

    expect(cleared.hasManagementCredentials, isFalse);
    expect(cleared.hasAgentCredentials, isTrue);
    expect(cleared.activeMode, ConnectionMode.agent);
  });
}
```

- [ ] **Step 2: Run auth model tests and verify RED**

Run:

```powershell
cd clients/flutter
flutter test test/features/auth/auth_models_test.dart
```

Expected: FAIL because `ConnectionMode`, `ManagementProfile`, `AgentProfile`, `hasManagementCredentials`, `hasAgentCredentials`, and `clearManagement` do not exist.

- [ ] **Step 3: Implement auth profile model**

In `clients/flutter/lib/features/auth/data/models/auth_models.dart`, replace the current `ClientProfile` definition with:

```dart
enum ConnectionMode { management, agent }

class ManagementProfile {
  const ManagementProfile({this.panelToken = ''});

  final String panelToken;

  bool get isConfigured => panelToken.trim().isNotEmpty;

  Map<String, dynamic> toJson() => {'panelToken': panelToken};

  factory ManagementProfile.fromJson(Map<String, dynamic>? json) {
    if (json == null) return const ManagementProfile();
    return ManagementProfile(panelToken: json['panelToken'] as String? ?? '');
  }
}

class AgentProfile {
  const AgentProfile({
    this.agentId = '',
    this.agentToken = '',
  });

  final String agentId;
  final String agentToken;

  bool get isRegistered => agentId.trim().isNotEmpty && agentToken.trim().isNotEmpty;

  Map<String, dynamic> toJson() => {
        'agentId': agentId,
        'agentToken': agentToken,
      };

  factory AgentProfile.fromJson(Map<String, dynamic>? json) {
    if (json == null) return const AgentProfile();
    return AgentProfile(
      agentId: json['agentId'] as String? ?? '',
      agentToken: json['agentToken'] as String? ?? '',
    );
  }
}

class ClientProfile {
  const ClientProfile({
    this.masterUrl = '',
    this.displayName = '',
    this.activeMode = ConnectionMode.agent,
    this.management = const ManagementProfile(),
    this.agent = const AgentProfile(),
  });

  final String masterUrl;
  final String displayName;
  final ConnectionMode activeMode;
  final ManagementProfile management;
  final AgentProfile agent;

  String get agentId => agent.agentId;
  String get token => agent.agentToken;
  bool get isRegistered => agent.isRegistered || management.isConfigured;
  bool get hasAgentCredentials => agent.isRegistered;
  bool get hasManagementCredentials => management.isConfigured;

  Map<String, dynamic> toJson() => {
        'masterUrl': masterUrl,
        'displayName': displayName,
        'activeMode': activeMode.name,
        'management': management.toJson(),
        'agent': agent.toJson(),
      };

  factory ClientProfile.fromJson(Map<String, dynamic> json) {
    final legacyAgentId = json['agentId'] as String? ?? '';
    final legacyToken = json['token'] as String? ?? '';
    final agent = json['agent'] is Map<String, dynamic>
        ? AgentProfile.fromJson(json['agent'] as Map<String, dynamic>)
        : AgentProfile(agentId: legacyAgentId, agentToken: legacyToken);
    final management = ManagementProfile.fromJson(
      json['management'] is Map<String, dynamic>
          ? json['management'] as Map<String, dynamic>
          : null,
    );
    final parsedMode = ConnectionMode.values
        .where((mode) => mode.name == json['activeMode'])
        .firstOrNull;
    final fallbackMode = management.isConfigured
        ? ConnectionMode.management
        : ConnectionMode.agent;
    return ClientProfile(
      masterUrl: json['masterUrl'] as String? ?? '',
      displayName: json['displayName'] as String? ?? '',
      activeMode: parsedMode ?? fallbackMode,
      management: management,
      agent: agent,
    );
  }

  ClientProfile copyWith({
    String? masterUrl,
    String? displayName,
    ConnectionMode? activeMode,
    ManagementProfile? management,
    AgentProfile? agent,
  }) =>
      ClientProfile(
        masterUrl: masterUrl ?? this.masterUrl,
        displayName: displayName ?? this.displayName,
        activeMode: activeMode ?? this.activeMode,
        management: management ?? this.management,
        agent: agent ?? this.agent,
      );

  ClientProfile clearManagement() {
    final nextMode = agent.isRegistered ? ConnectionMode.agent : ConnectionMode.management;
    return copyWith(
      activeMode: nextMode,
      management: const ManagementProfile(),
    );
  }

  ClientProfile clearAgent() {
    final nextMode = management.isConfigured ? ConnectionMode.management : ConnectionMode.agent;
    return copyWith(
      activeMode: nextMode,
      agent: const AgentProfile(),
    );
  }
}
```

Keep the existing `AuthState` classes below this model.

- [ ] **Step 4: Verify auth model tests pass**

Run:

```powershell
cd clients/flutter
flutter test test/features/auth/auth_models_test.dart
```

Expected: PASS.

- [ ] **Step 5: Write failing auth provider tests**

Create `clients/flutter/test/features/auth/auth_provider_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/features/auth/data/models/auth_models.dart';
import 'package:nre_client/features/auth/data/repositories/auth_repository.dart';
import 'package:nre_client/features/auth/presentation/providers/auth_provider.dart';
import 'package:riverpod/riverpod.dart';

class MockAuthRepository extends Mock implements AuthRepository {}

void main() {
  test('connectManagement saves panel token profile without agent credentials', () async {
    final repo = MockAuthRepository();
    when(repo.loadProfile).thenAnswer((_) async => const ClientProfile());
    when(() => repo.saveProfile(any())).thenAnswer((_) async {});
    final container = ProviderContainer(
      overrides: [authRepositoryProvider.overrideWithValue(repo)],
    );
    addTearDown(container.dispose);

    await container.read(authNotifierProvider.future);
    await container.read(authNotifierProvider.notifier).connectManagement(
          masterUrl: 'https://panel.example.com/panel-api/',
          panelToken: 'panel-secret',
          name: 'ops-laptop',
        );

    final state = container.read(authNotifierProvider).value;
    expect(state, isA<AuthStateAuthenticated>());
    final profile = (state as AuthStateAuthenticated).profile;
    expect(profile.masterUrl, 'https://panel.example.com');
    expect(profile.activeMode, ConnectionMode.management);
    expect(profile.management.panelToken, 'panel-secret');
    expect(profile.hasAgentCredentials, isFalse);
    verify(() => repo.saveProfile(any(that: isA<ClientProfile>()))).called(1);
  });

  test('clearManagement preserves agent profile', () async {
    final repo = MockAuthRepository();
    final loaded = ClientProfile(
      masterUrl: 'https://panel.example.com',
      activeMode: ConnectionMode.management,
      management: const ManagementProfile(panelToken: 'panel-secret'),
      agent: const AgentProfile(agentId: 'agent-1', agentToken: 'agent-secret'),
    );
    when(repo.loadProfile).thenAnswer((_) async => loaded);
    when(() => repo.saveProfile(any())).thenAnswer((_) async {});
    final container = ProviderContainer(
      overrides: [authRepositoryProvider.overrideWithValue(repo)],
    );
    addTearDown(container.dispose);

    await container.read(authNotifierProvider.future);
    await container.read(authNotifierProvider.notifier).clearManagement();

    final state = container.read(authNotifierProvider).value;
    final profile = (state as AuthStateAuthenticated).profile;
    expect(profile.hasManagementCredentials, isFalse);
    expect(profile.agent.agentId, 'agent-1');
    expect(profile.activeMode, ConnectionMode.agent);
  });
}
```

- [ ] **Step 6: Run auth provider tests and verify RED**

Run:

```powershell
cd clients/flutter
flutter test test/features/auth/auth_provider_test.dart
```

Expected: FAIL because `connectManagement` and `clearManagement` are not implemented on `AuthNotifier`.

- [ ] **Step 7: Implement auth provider management methods**

In `clients/flutter/lib/features/auth/presentation/providers/auth_provider.dart`:

- Keep `register` as the agent-mode registration path.
- Change saved agent profile construction in `register` to use `activeMode: ConnectionMode.agent` and `AgentProfile`.
- Add:

```dart
  Future<void> connectManagement({
    required String masterUrl,
    required String panelToken,
    required String name,
  }) async {
    state = const AsyncData(AuthStateLoading());
    try {
      final normalizedUrl = normalizeMasterUrl(masterUrl);
      final token = panelToken.trim();
      if (token.isEmpty) {
        throw const FormatException('Panel token is required');
      }
      final current = await ref.read(authRepositoryProvider).loadProfile();
      final profile = current.copyWith(
        masterUrl: normalizedUrl,
        displayName: name.trim().isEmpty ? current.displayName : name.trim(),
        activeMode: ConnectionMode.management,
        management: ManagementProfile(panelToken: token),
      );
      await ref.read(authRepositoryProvider).saveProfile(profile);
      state = AsyncData(AuthStateAuthenticated(profile));
    } catch (e) {
      state = AsyncData(AuthStateError(e.toString()));
    }
  }

  Future<void> clearManagement() async {
    final repo = ref.read(authRepositoryProvider);
    final current = await repo.loadProfile();
    final next = current.clearManagement();
    if (next.hasAgentCredentials || next.hasManagementCredentials) {
      await repo.saveProfile(next);
      state = AsyncData(AuthStateAuthenticated(next));
    } else {
      await repo.clearProfile();
      state = const AsyncData(AuthStateUnauthenticated());
    }
  }
```

When updating `register`, construct:

```dart
final profile = current.copyWith(
  masterUrl: normalizedUrl,
  displayName: name.trim(),
  activeMode: ConnectionMode.agent,
  agent: AgentProfile(agentId: agentId, agentToken: agentToken),
);
```

- [ ] **Step 8: Verify auth tests pass**

Run:

```powershell
cd clients/flutter
flutter test test/features/auth/auth_models_test.dart test/features/auth/auth_provider_test.dart
```

Expected: PASS.

- [ ] **Step 9: Commit auth profile work**

Run:

```powershell
git add clients/flutter/lib/features/auth/data/models/auth_models.dart clients/flutter/lib/features/auth/presentation/providers/auth_provider.dart clients/flutter/test/features/auth/auth_models_test.dart clients/flutter/test/features/auth/auth_provider_test.dart
git commit -m "feat(flutter): support management and agent profiles"
```

---

## Task 2: Typed Panel API Client and Core Models

**Files:**
- Create: `clients/flutter/lib/core/network/panel_api_client.dart`
- Modify: `clients/flutter/lib/core/network/dio_client.dart`
- Modify: `clients/flutter/lib/features/rules/data/models/rule_models.dart`
- Create: `clients/flutter/lib/features/agents/data/models/agent_models.dart`
- Modify: `clients/flutter/lib/features/certificates/data/models/certificate_models.dart`
- Modify: `clients/flutter/lib/features/relay/data/models/relay_models.dart`
- Test: `clients/flutter/test/core/network/panel_api_client_test.dart`
- Test: `clients/flutter/test/features/rules/rule_models_test.dart`
- Test: `clients/flutter/test/features/agents/agent_models_test.dart`
- Test: `clients/flutter/test/features/certificates/certificate_models_test.dart`
- Test: `clients/flutter/test/features/relay/relay_models_test.dart`

- [ ] **Step 1: Write failing model normalization tests**

Create `clients/flutter/test/features/rules/rule_models_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/features/rules/data/models/rule_models.dart';

void main() {
  test('HttpProxyRule normalizes backend_url into backends', () {
    final rule = HttpProxyRule.fromJson({
      'id': 7,
      'frontend_url': 'https://emby.example.com',
      'backend_url': 'http://emby:8096',
      'enabled': true,
      'load_balancing': {'strategy': 'round_robin'},
    });

    expect(rule.id, '7');
    expect(rule.frontendUrl, 'https://emby.example.com');
    expect(rule.backendUrl, 'http://emby:8096');
    expect(rule.backends.single.url, 'http://emby:8096');
    expect(rule.loadBalancingStrategy, 'round_robin');
  });

  test('CreateHttpRuleRequest emits backend-compatible payload', () {
    final request = CreateHttpRuleRequest(
      frontendUrl: 'https://emby.example.com',
      backends: const [HttpBackend(url: 'http://emby:8096')],
      enabled: false,
      tags: const ['media'],
      proxyRedirect: true,
      passProxyHeaders: true,
      userAgent: 'NRE-Test',
      customHeaders: const [HttpHeaderEntry(name: 'X-Test', value: 'yes')],
      loadBalancingStrategy: 'adaptive',
      relayLayers: const [[1, 2]],
      relayObfs: true,
    );

    expect(request.toJson(), {
      'frontend_url': 'https://emby.example.com',
      'backend_url': 'http://emby:8096',
      'backends': [{'url': 'http://emby:8096'}],
      'enabled': false,
      'tags': ['media'],
      'proxy_redirect': true,
      'pass_proxy_headers': true,
      'user_agent': 'NRE-Test',
      'custom_headers': [{'name': 'X-Test', 'value': 'yes'}],
      'load_balancing': {'strategy': 'adaptive'},
      'relay_layers': [[1, 2]],
      'relay_obfs': true,
    });
  });
}
```

Create `clients/flutter/test/features/agents/agent_models_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/features/agents/data/models/agent_models.dart';

void main() {
  test('AgentSummary parses status and revision fields', () {
    final agent = AgentSummary.fromJson({
      'id': 'agent-1',
      'name': 'edge-a',
      'status': 'online',
      'mode': 'pull',
      'platform': 'windows',
      'version': '2.1.0',
      'last_seen': '2026-05-01T10:00:00Z',
      'current_revision': 3,
      'target_revision': 5,
      'tags': ['edge'],
      'capabilities': ['http_rules'],
    });

    expect(agent.id, 'agent-1');
    expect(agent.isOnline, isTrue);
    expect(agent.hasPendingRevision, isTrue);
    expect(agent.tags, ['edge']);
  });
}
```

Create `clients/flutter/test/features/certificates/certificate_models_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/features/certificates/data/models/certificate_models.dart';

void main() {
  test('Certificate parses backend certificate metadata', () {
    final cert = Certificate.fromJson({
      'id': 21,
      'domain': 'emby.example.com',
      'scope': 'domain',
      'issuer_mode': 'local_http01',
      'certificate_type': 'acme',
      'status': 'active',
      'expires_at': '2026-06-01T00:00:00Z',
      'issued_at': '2026-03-01T00:00:00Z',
      'self_signed': false,
      'fingerprint': 'abc',
      'target_agent_ids': ['local'],
    });

    expect(cert.id, '21');
    expect(cert.domain, 'emby.example.com');
    expect(cert.issuerMode, 'local_http01');
    expect(cert.targetAgentIds, ['local']);
  });
}
```

Create `clients/flutter/test/features/relay/relay_models_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/features/relay/data/models/relay_models.dart';

void main() {
  test('RelayListener derives listen address from bind hosts and port', () {
    final listener = RelayListener.fromJson({
      'id': 2,
      'agent_id': 'agent-1',
      'agent_name': 'edge-a',
      'name': 'public-tls',
      'listen_port': 8443,
      'bind_hosts': ['0.0.0.0'],
      'enabled': true,
      'tls_mode': 'ca_only',
      'certificate_source': 'existing_certificate',
      'certificate_id': 21,
    });

    expect(listener.id, '2');
    expect(listener.listenAddress, '0.0.0.0:8443');
    expect(listener.agentName, 'edge-a');
    expect(listener.certificateId, '21');
  });
}
```

- [ ] **Step 2: Run model tests and verify RED**

Run:

```powershell
cd clients/flutter
flutter test test/features/rules/rule_models_test.dart test/features/agents/agent_models_test.dart test/features/certificates/certificate_models_test.dart test/features/relay/relay_models_test.dart
```

Expected: FAIL because the new typed classes and fields do not exist.

- [ ] **Step 3: Implement typed models**

Implement the model APIs exercised by the tests:

- `HttpBackend`, `HttpHeaderEntry`, `HttpProxyRule`, `CreateHttpRuleRequest`, `UpdateHttpRuleRequest`.
- `ProxyRule` should remain as a compatibility alias or wrapper for current UI callers until Task 4 migrates them.
- `AgentSummary`.
- Extended `Certificate` fields and create/update request classes.
- Extended `RelayListener`, `CreateRelayListenerRequest`, `UpdateRelayListenerRequest`.

Use these parsing rules in every model:

```dart
String stringId(dynamic value) => value?.toString() ?? '';
List<String> stringList(dynamic value) =>
    value is List ? value.map((item) => item.toString()).toList() : const [];
DateTime? dateTimeOrNull(dynamic value) =>
    value is String ? DateTime.tryParse(value) : null;
```

- [ ] **Step 4: Verify model tests pass**

Run:

```powershell
cd clients/flutter
flutter test test/features/rules/rule_models_test.dart test/features/agents/agent_models_test.dart test/features/certificates/certificate_models_test.dart test/features/relay/relay_models_test.dart
```

Expected: PASS.

- [ ] **Step 5: Write failing PanelApiClient tests**

Create `clients/flutter/test/core/network/panel_api_client_test.dart`:

```dart
import 'dart:convert';
import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/features/rules/data/models/rule_models.dart';

void main() {
  test('fetchAgents sends X-Panel-Token and parses agents envelope', () async {
    late HttpRequest captured;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      captured = request;
      request.response
        ..headers.contentType = ContentType.json
        ..write(jsonEncode({
          'ok': true,
          'agents': [
            {'id': 'agent-1', 'name': 'edge-a', 'status': 'online'}
          ],
        }));
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    final agents = await api.fetchAgents();

    expect(captured.uri.path, '/panel-api/agents');
    expect(captured.headers.value('X-Panel-Token'), 'panel-secret');
    expect(agents.single.id, 'agent-1');
  });

  test('createRule posts normalized payload to selected agent', () async {
    late Map<String, dynamic> body;
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      expect(request.method, 'POST');
      expect(request.uri.path, '/panel-api/agents/local/rules');
      body = jsonDecode(await utf8.decoder.bind(request).join()) as Map<String, dynamic>;
      request.response
        ..headers.contentType = ContentType.json
        ..write(jsonEncode({
          'ok': true,
          'rule': {
            'id': 9,
            'frontend_url': body['frontend_url'],
            'backend_url': body['backend_url'],
            'enabled': body['enabled'],
          },
        }));
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'panel-secret',
    );

    final rule = await api.createRule(
      'local',
      const CreateHttpRuleRequest(
        frontendUrl: 'https://emby.example.com',
        backends: [HttpBackend(url: 'http://emby:8096')],
      ),
    );

    expect(body['backend_url'], 'http://emby:8096');
    expect(rule.id, '9');
  });

  test('throws PanelApiException with backend message', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(server.close);
    server.listen((request) async {
      request.response
        ..statusCode = HttpStatus.forbidden
        ..headers.contentType = ContentType.json
        ..write(jsonEncode({'error': 'panel token denied'}));
      await request.response.close();
    });

    final api = PanelApiClient(
      baseUrl: 'http://${server.address.host}:${server.port}',
      panelToken: 'bad-token',
    );

    expect(
      api.fetchAgents,
      throwsA(isA<PanelApiException>().having((e) => e.message, 'message', 'panel token denied')),
    );
  });
}
```

- [ ] **Step 6: Run PanelApiClient tests and verify RED**

Run:

```powershell
cd clients/flutter
flutter test test/core/network/panel_api_client_test.dart
```

Expected: FAIL because `PanelApiClient` does not exist.

- [ ] **Step 7: Implement PanelApiClient**

Create `clients/flutter/lib/core/network/panel_api_client.dart` with:

```dart
import 'package:dio/dio.dart';

import '../../features/agents/data/models/agent_models.dart';
import '../../features/certificates/data/models/certificate_models.dart';
import '../../features/relay/data/models/relay_models.dart';
import '../../features/rules/data/models/rule_models.dart';

class PanelApiException implements Exception {
  const PanelApiException(this.message, {this.statusCode});
  final String message;
  final int? statusCode;

  @override
  String toString() => message;
}

class PanelApiClient {
  PanelApiClient({
    required String baseUrl,
    required String panelToken,
    Dio? dio,
  }) : _dio = dio ??
            Dio(
              BaseOptions(
                baseUrl: _normalizeBaseUrl(baseUrl),
                connectTimeout: const Duration(seconds: 10),
                receiveTimeout: const Duration(seconds: 10),
                headers: {'X-Panel-Token': panelToken},
              ),
            );

  final Dio _dio;

  static String _normalizeBaseUrl(String value) {
    final trimmed = value.trim().replaceAll(RegExp(r'/+$'), '');
    if (trimmed.endsWith('/panel-api')) return trimmed;
    return '$trimmed/panel-api';
  }

  Options get _longRunning => Options(
        sendTimeout: const Duration(minutes: 2),
        receiveTimeout: const Duration(minutes: 2),
      );

  Future<List<AgentSummary>> fetchAgents() async {
    final data = await _requestMap(() => _dio.get('/agents'));
    return _list(data, 'agents').map(AgentSummary.fromJson).toList();
  }

  Future<List<HttpProxyRule>> fetchRules(String agentId) async {
    final data = await _requestMap(() => _dio.get('/agents/${Uri.encodeComponent(agentId)}/rules'));
    return _list(data, 'rules').map(HttpProxyRule.fromJson).toList();
  }

  Future<HttpProxyRule> createRule(String agentId, CreateHttpRuleRequest request) async {
    final data = await _requestMap(
      () => _dio.post(
        '/agents/${Uri.encodeComponent(agentId)}/rules',
        data: request.toJson(),
        options: _longRunning,
      ),
    );
    return HttpProxyRule.fromJson(_object(data, 'rule'));
  }

  Future<HttpProxyRule> updateRule(String agentId, String id, UpdateHttpRuleRequest request) async {
    final data = await _requestMap(
      () => _dio.put(
        '/agents/${Uri.encodeComponent(agentId)}/rules/${Uri.encodeComponent(id)}',
        data: request.toJson(),
        options: _longRunning,
      ),
    );
    return HttpProxyRule.fromJson(_object(data, 'rule'));
  }

  Future<void> deleteRule(String agentId, String id) async {
    await _requestMap(
      () => _dio.delete(
        '/agents/${Uri.encodeComponent(agentId)}/rules/${Uri.encodeComponent(id)}',
        options: _longRunning,
      ),
    );
  }

  Future<void> applyConfig(String agentId) async {
    await _requestMap(
      () => _dio.post('/agents/${Uri.encodeComponent(agentId)}/apply', data: {}, options: _longRunning),
    );
  }

  Future<List<Certificate>> fetchCertificates(String agentId) async {
    final data = await _requestMap(() => _dio.get('/agents/${Uri.encodeComponent(agentId)}/certificates'));
    return _list(data, 'certificates').map(Certificate.fromJson).toList();
  }

  Future<List<RelayListener>> fetchRelayListeners(String agentId) async {
    final data = await _requestMap(() => _dio.get('/agents/${Uri.encodeComponent(agentId)}/relay-listeners'));
    return _list(data, 'listeners').map((item) => RelayListener.fromJson({...item, 'agent_id': agentId})).toList();
  }

  Future<Map<String, dynamic>> _requestMap(Future<Response<dynamic>> Function() request) async {
    try {
      final response = await request();
      final data = response.data;
      if (data is Map<String, dynamic>) return data;
      if (data is Map) return Map<String, dynamic>.from(data);
      throw const PanelApiException('Invalid backend response');
    } on DioException catch (error) {
      final data = error.response?.data;
      final message = data is Map
          ? (data['error'] ?? data['message'])?.toString()
          : null;
      throw PanelApiException(
        message ?? 'Request failed with HTTP ${error.response?.statusCode ?? 0}',
        statusCode: error.response?.statusCode,
      );
    }
  }

  List<Map<String, dynamic>> _list(Map<String, dynamic> data, String key) {
    final raw = data[key];
    if (raw is! List) return const [];
    return raw.whereType<Map>().map((item) => Map<String, dynamic>.from(item)).toList();
  }

  Map<String, dynamic> _object(Map<String, dynamic> data, String key) {
    final raw = data[key];
    if (raw is Map<String, dynamic>) return raw;
    if (raw is Map) return Map<String, dynamic>.from(raw);
    throw PanelApiException('Backend response missing $key');
  }
}
```

Do not add L4, version policy, package, backup, or worker endpoints in this task.

- [ ] **Step 8: Verify API client tests pass**

Run:

```powershell
cd clients/flutter
flutter test test/core/network/panel_api_client_test.dart
```

Expected: PASS.

- [ ] **Step 9: Commit API/model work**

Run:

```powershell
git add clients/flutter/lib/core/network/panel_api_client.dart clients/flutter/lib/core/network/dio_client.dart clients/flutter/lib/features/rules/data/models/rule_models.dart clients/flutter/lib/features/agents/data/models/agent_models.dart clients/flutter/lib/features/certificates/data/models/certificate_models.dart clients/flutter/lib/features/relay/data/models/relay_models.dart clients/flutter/test/core/network/panel_api_client_test.dart clients/flutter/test/features/rules/rule_models_test.dart clients/flutter/test/features/agents/agent_models_test.dart clients/flutter/test/features/certificates/certificate_models_test.dart clients/flutter/test/features/relay/relay_models_test.dart
git commit -m "feat(flutter): add typed panel api models"
```

---

## Task 3: Riverpod Stores Use Panel API

**Files:**
- Modify: `clients/flutter/lib/features/rules/presentation/providers/rules_provider.dart`
- Create: `clients/flutter/lib/features/agents/presentation/providers/agents_provider.dart`
- Modify: `clients/flutter/lib/features/certificates/presentation/providers/certificates_provider.dart`
- Modify: `clients/flutter/lib/features/relay/presentation/providers/relay_provider.dart`
- Create: `clients/flutter/lib/features/dashboard/presentation/providers/dashboard_provider.dart`
- Test: `clients/flutter/test/features/rules/rules_provider_test.dart`
- Test: `clients/flutter/test/features/agents/agents_provider_test.dart`
- Test: `clients/flutter/test/features/relay/relay_provider_test.dart`

- [ ] **Step 1: Write failing provider tests for optimistic rollback**

Create `clients/flutter/test/features/rules/rules_provider_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/features/rules/data/models/rule_models.dart';
import 'package:nre_client/features/rules/presentation/providers/rules_provider.dart';
import 'package:riverpod/riverpod.dart';

class FakeRulesApi extends Mock implements PanelApiClient {}

void main() {
  test('toggleRule rolls back when backend update fails', () async {
    final api = FakeRulesApi();
    when(() => api.fetchRules('local')).thenAnswer(
      (_) async => const [
        HttpProxyRule(
          id: '1',
          frontendUrl: 'https://emby.example.com',
          backendUrl: 'http://emby:8096',
          backends: [HttpBackend(url: 'http://emby:8096')],
          enabled: true,
        ),
      ],
    );
    when(() => api.updateRule('local', '1', any())).thenThrow(const PanelApiException('failed'));
    final container = ProviderContainer(
      overrides: [
        selectedAgentIdProvider.overrideWithValue('local'),
        panelApiClientProvider.overrideWithValue(api),
      ],
    );
    addTearDown(container.dispose);

    await container.read(rulesListProvider.future);
    await expectLater(
      container.read(rulesListProvider.notifier).toggleRule('1', false),
      throwsA(isA<PanelApiException>()),
    );

    expect(container.read(rulesListProvider).value!.single.enabled, isTrue);
  });
}
```

- [ ] **Step 2: Run provider test and verify RED**

Run:

```powershell
cd clients/flutter
flutter test test/features/rules/rules_provider_test.dart
```

Expected: FAIL because `panelApiClientProvider` and `selectedAgentIdProvider` are not wired, and rules provider still uses `ApiClient`.

- [ ] **Step 3: Implement shared panel API provider and selected agent provider**

In `clients/flutter/lib/features/rules/presentation/providers/rules_provider.dart`, add or import from a shared provider file:

```dart
@riverpod
PanelApiClient panelApiClient(PanelApiClientRef ref) {
  final authState = ref.watch(authNotifierProvider).valueOrNull;
  if (authState is! AuthStateAuthenticated ||
      !authState.profile.hasManagementCredentials) {
    throw const PanelApiException('Management profile is not configured');
  }
  return PanelApiClient(
    baseUrl: authState.profile.masterUrl,
    panelToken: authState.profile.management.panelToken,
  );
}

@riverpod
String selectedAgentId(SelectedAgentIdRef ref) => 'local';
```

Keep the provider in one shared location if later features import it. A practical path is `clients/flutter/lib/core/network/panel_api_provider.dart`; if created, update this task's imports and generated files accordingly.

- [ ] **Step 4: Refactor rules provider to `HttpProxyRule`**

Change `RulesList` to:

```dart
@riverpod
class RulesList extends _$RulesList {
  @override
  Future<List<HttpProxyRule>> build() async {
    final api = ref.read(panelApiClientProvider);
    final agentId = ref.watch(selectedAgentIdProvider);
    return api.fetchRules(agentId);
  }

  Future<void> toggleRule(String id, bool enabled) async {
    final previous = state.value ?? [];
    state = AsyncData([
      for (final rule in previous)
        rule.id == id ? rule.copyWith(enabled: enabled) : rule,
    ]);
    try {
      final api = ref.read(panelApiClientProvider);
      final agentId = ref.read(selectedAgentIdProvider);
      final existing = previous.firstWhere((rule) => rule.id == id);
      await api.updateRule(
        agentId,
        id,
        UpdateHttpRuleRequest.fromRule(existing.copyWith(enabled: enabled)),
      );
    } catch (_) {
      state = AsyncData(previous);
      rethrow;
    }
  }
}
```

Add `createRule`, `updateRule`, and `deleteRule` with the same previous-state rollback pattern.

- [ ] **Step 5: Generate Riverpod files**

Run:

```powershell
cd clients/flutter
dart run build_runner build --delete-conflicting-outputs
```

Expected: generated provider files update without build errors.

- [ ] **Step 6: Verify rules provider test passes**

Run:

```powershell
cd clients/flutter
flutter test test/features/rules/rules_provider_test.dart
```

Expected: PASS.

- [ ] **Step 7: Add agent provider tests**

Create `clients/flutter/test/features/agents/agents_provider_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/features/agents/data/models/agent_models.dart';
import 'package:nre_client/features/agents/presentation/providers/agents_provider.dart';
import 'package:nre_client/features/rules/presentation/providers/rules_provider.dart';
import 'package:riverpod/riverpod.dart';

class FakeAgentsApi extends Mock implements PanelApiClient {}

void main() {
  test('agentsList loads remote agents', () async {
    final api = FakeAgentsApi();
    when(api.fetchAgents).thenAnswer(
      (_) async => const [AgentSummary(id: 'agent-1', name: 'edge-a', status: 'online')],
    );
    final container = ProviderContainer(
      overrides: [panelApiClientProvider.overrideWithValue(api)],
    );
    addTearDown(container.dispose);

    final agents = await container.read(agentsListProvider.future);

    expect(agents.single.name, 'edge-a');
  });
}
```

- [ ] **Step 8: Implement agents provider**

Create `clients/flutter/lib/features/agents/presentation/providers/agents_provider.dart`:

```dart
import 'package:riverpod_annotation/riverpod_annotation.dart';

import '../../../../core/network/panel_api_client.dart';
import '../../../rules/presentation/providers/rules_provider.dart';
import '../../data/models/agent_models.dart';

part 'agents_provider.g.dart';

@riverpod
class AgentsList extends _$AgentsList {
  @override
  Future<List<AgentSummary>> build() async {
    final api = ref.read(panelApiClientProvider);
    return api.fetchAgents();
  }

  Future<void> refresh() async {
    state = const AsyncLoading();
    state = await AsyncValue.guard(() => ref.read(panelApiClientProvider).fetchAgents());
  }
}
```

- [ ] **Step 9: Write failing provider tests for agent actions**

Append to `clients/flutter/test/features/agents/agents_provider_test.dart`:

```dart
  test('deleteAgent removes item optimistically and rolls back on failure', () async {
    final api = FakeAgentsApi();
    when(api.fetchAgents).thenAnswer(
      (_) async => const [AgentSummary(id: 'agent-1', name: 'edge-a', status: 'online')],
    );
    when(() => api.deleteAgent('agent-1')).thenThrow(const PanelApiException('delete failed'));
    final container = ProviderContainer(
      overrides: [panelApiClientProvider.overrideWithValue(api)],
    );
    addTearDown(container.dispose);

    await container.read(agentsListProvider.future);
    await expectLater(
      container.read(agentsListProvider.notifier).deleteAgent('agent-1'),
      throwsA(isA<PanelApiException>()),
    );

    expect(container.read(agentsListProvider).value!.single.id, 'agent-1');
  });
```

Run:

```powershell
cd clients/flutter
flutter test test/features/agents/agents_provider_test.dart
```

Expected: FAIL because `AgentsList.deleteAgent` and `PanelApiClient.deleteAgent` do not exist.

- [ ] **Step 10: Implement agent provider actions**

Extend `PanelApiClient`:

```dart
Future<AgentSummary> renameAgent(String agentId, String name) async {
  final data = await _requestMap(
    () => _dio.patch('/agents/${Uri.encodeComponent(agentId)}', data: {'name': name}),
  );
  return AgentSummary.fromJson(_object(data, 'agent'));
}

Future<void> deleteAgent(String agentId) async {
  await _requestMap(() => _dio.delete('/agents/${Uri.encodeComponent(agentId)}'));
}

Future<void> applyConfig(String agentId) async {
  await _requestMap(
    () => _dio.post('/agents/${Uri.encodeComponent(agentId)}/apply', data: {}, options: _longRunning),
  );
}
```

Extend `AgentsList`:

```dart
Future<void> deleteAgent(String agentId) async {
  final previous = state.value ?? [];
  state = AsyncData(previous.where((agent) => agent.id != agentId).toList());
  try {
    await ref.read(panelApiClientProvider).deleteAgent(agentId);
  } catch (_) {
    state = AsyncData(previous);
    rethrow;
  }
}

Future<void> renameAgent(String agentId, String name) async {
  final api = ref.read(panelApiClientProvider);
  final updated = await api.renameAgent(agentId, name);
  final current = state.value ?? [];
  state = AsyncData([
    for (final agent in current) agent.id == agentId ? updated : agent,
  ]);
}

Future<void> applyConfig(String agentId) {
  return ref.read(panelApiClientProvider).applyConfig(agentId);
}
```

- [ ] **Step 11: Write failing certificate provider test**

Create `clients/flutter/test/features/certificates/certificates_provider_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/features/certificates/data/models/certificate_models.dart';
import 'package:nre_client/features/certificates/presentation/providers/certificates_provider.dart';
import 'package:nre_client/features/rules/presentation/providers/rules_provider.dart';
import 'package:riverpod/riverpod.dart';

class FakeCertificatesApi extends Mock implements PanelApiClient {}

void main() {
  test('certificatesList loads certificates for selected agent', () async {
    final api = FakeCertificatesApi();
    when(() => api.fetchCertificates('local')).thenAnswer(
      (_) async => const [Certificate(id: '21', domain: 'emby.example.com')],
    );
    final container = ProviderContainer(
      overrides: [
        selectedAgentIdProvider.overrideWithValue('local'),
        panelApiClientProvider.overrideWithValue(api),
      ],
    );
    addTearDown(container.dispose);

    final certs = await container.read(certificatesListProvider.future);

    expect(certs.single.domain, 'emby.example.com');
  });
}
```

Run:

```powershell
cd clients/flutter
flutter test test/features/certificates/certificates_provider_test.dart
```

Expected: FAIL because certificates provider still uses the old `ApiClient` path.

- [ ] **Step 12: Implement certificate provider with panel API**

Update `certificates_provider.dart` so `CertificatesList.build` reads `panelApiClientProvider` and `selectedAgentIdProvider`, then returns `api.fetchCertificates(agentId)`.

Add methods:

```dart
Future<Certificate> issueCertificate(String id) async {
  final api = ref.read(panelApiClientProvider);
  final agentId = ref.read(selectedAgentIdProvider);
  final updated = await api.issueCertificate(agentId, id);
  final current = state.value ?? [];
  state = AsyncData([
    for (final cert in current) cert.id == id ? updated : cert,
  ]);
  return updated;
}

Future<void> deleteCertificate(String id) async {
  final previous = state.value ?? [];
  state = AsyncData(previous.where((cert) => cert.id != id).toList());
  try {
    await ref.read(panelApiClientProvider).deleteCertificate(ref.read(selectedAgentIdProvider), id);
  } catch (_) {
    state = AsyncData(previous);
    rethrow;
  }
}
```

Extend `PanelApiClient` with:

```dart
Future<Certificate> issueCertificate(String agentId, String id) async {
  final data = await _requestMap(
    () => _dio.post(
      '/agents/${Uri.encodeComponent(agentId)}/certificates/${Uri.encodeComponent(id)}/issue',
      data: {},
      options: _longRunning,
    ),
  );
  return Certificate.fromJson(_object(data, 'certificate'));
}

Future<void> deleteCertificate(String agentId, String id) async {
  await _requestMap(
    () => _dio.delete(
      '/agents/${Uri.encodeComponent(agentId)}/certificates/${Uri.encodeComponent(id)}',
      options: _longRunning,
    ),
  );
}
```

- [ ] **Step 13: Write failing relay provider test**

Create `clients/flutter/test/features/relay/relay_provider_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:mocktail/mocktail.dart';
import 'package:nre_client/core/network/panel_api_client.dart';
import 'package:nre_client/features/relay/data/models/relay_models.dart';
import 'package:nre_client/features/relay/presentation/providers/relay_provider.dart';
import 'package:nre_client/features/rules/presentation/providers/rules_provider.dart';
import 'package:riverpod/riverpod.dart';

class FakeRelayApi extends Mock implements PanelApiClient {}

void main() {
  test('toggleRelay rolls back when update fails', () async {
    final api = FakeRelayApi();
    when(() => api.fetchRelayListeners('local')).thenAnswer(
      (_) async => const [RelayListener(id: '2', listenAddress: '0.0.0.0:8443', protocol: 'TCP')],
    );
    when(() => api.updateRelayListener('local', '2', any())).thenThrow(const PanelApiException('failed'));
    final container = ProviderContainer(
      overrides: [
        selectedAgentIdProvider.overrideWithValue('local'),
        panelApiClientProvider.overrideWithValue(api),
      ],
    );
    addTearDown(container.dispose);

    await container.read(relayListProvider.future);
    await expectLater(
      container.read(relayListProvider.notifier).toggleRelay('2', false),
      throwsA(isA<PanelApiException>()),
    );

    expect(container.read(relayListProvider).value!.single.enabled, isTrue);
  });
}
```

Run:

```powershell
cd clients/flutter
flutter test test/features/relay/relay_provider_test.dart
```

Expected: FAIL because relay provider still uses the old `ApiClient` path and `PanelApiClient.updateRelayListener` does not exist.

- [ ] **Step 14: Implement relay provider with panel API**

Update `relay_provider.dart`:

```dart
@override
Future<List<RelayListener>> build() {
  final api = ref.read(panelApiClientProvider);
  final agentId = ref.watch(selectedAgentIdProvider);
  return api.fetchRelayListeners(agentId);
}
```

Implement `toggleRelay`, `createRelay`, `updateRelay`, and `deleteRelay` through `PanelApiClient`, using previous-state rollback for toggle and delete.

Extend `PanelApiClient`:

```dart
Future<RelayListener> createRelayListener(String agentId, CreateRelayListenerRequest request) async {
  final data = await _requestMap(
    () => _dio.post(
      '/agents/${Uri.encodeComponent(agentId)}/relay-listeners',
      data: request.toJson(),
      options: _longRunning,
    ),
  );
  return RelayListener.fromJson(_object(data, 'listener'));
}

Future<RelayListener> updateRelayListener(String agentId, String id, UpdateRelayListenerRequest request) async {
  final data = await _requestMap(
    () => _dio.put(
      '/agents/${Uri.encodeComponent(agentId)}/relay-listeners/${Uri.encodeComponent(id)}',
      data: request.toJson(),
      options: _longRunning,
    ),
  );
  return RelayListener.fromJson(_object(data, 'listener'));
}

Future<void> deleteRelayListener(String agentId, String id) async {
  await _requestMap(
    () => _dio.delete(
      '/agents/${Uri.encodeComponent(agentId)}/relay-listeners/${Uri.encodeComponent(id)}',
      options: _longRunning,
    ),
  );
}
```

- [ ] **Step 15: Verify provider suite**

Run:

```powershell
cd clients/flutter
dart run build_runner build --delete-conflicting-outputs
flutter test test/features/rules/rules_provider_test.dart test/features/agents/agents_provider_test.dart test/features/certificates/certificates_provider_test.dart test/features/relay/relay_provider_test.dart
```

Expected: PASS.

- [ ] **Step 16: Commit provider work**

Run:

```powershell
git add clients/flutter/lib clients/flutter/test/features
git commit -m "feat(flutter): wire daily crud providers to panel api"
```

---

## Task 4: Connect and Settings Screens Expose Both Modes

**Files:**
- Modify: `clients/flutter/lib/features/auth/presentation/screens/connect_screen.dart`
- Modify: `clients/flutter/lib/features/settings/presentation/screens/settings_screen.dart`
- Modify: `clients/flutter/lib/l10n/app_en.arb`
- Modify: `clients/flutter/lib/l10n/app_zh.arb`
- Regenerate: `clients/flutter/lib/l10n/app_localizations*.dart`
- Test: `clients/flutter/test/features/auth/connect_screen_test.dart`
- Test: `clients/flutter/test/features/settings/settings_screen_test.dart`

- [ ] **Step 1: Write failing connect screen test**

Create `clients/flutter/test/features/auth/connect_screen_test.dart`:

```dart
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:nre_client/features/auth/presentation/screens/connect_screen.dart';
import 'package:nre_client/l10n/app_localizations.dart';

void main() {
  testWidgets('connect screen lets user choose management mode', (tester) async {
    await tester.pumpWidget(
      const ProviderScope(
        child: MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: ConnectScreen(),
        ),
      ),
    );

    await tester.pumpAndSettle();

    expect(find.text('Management'), findsOneWidget);
    expect(find.text('Agent'), findsOneWidget);
    await tester.tap(find.text('Management'));
    await tester.pumpAndSettle();
    expect(find.text('Panel token'), findsOneWidget);
  });
}
```

- [ ] **Step 2: Run connect screen test and verify RED**

Run:

```powershell
cd clients/flutter
flutter test test/features/auth/connect_screen_test.dart
```

Expected: FAIL because the screen only has the current three-step agent registration flow.

- [ ] **Step 3: Implement mode selector in ConnectScreen**

Add a local `_mode` field:

```dart
ConnectionMode _mode = ConnectionMode.management;
```

Render two selectable buttons above the form:

```dart
SegmentedButton<ConnectionMode>(
  segments: const [
    ButtonSegment(value: ConnectionMode.management, label: Text('Management')),
    ButtonSegment(value: ConnectionMode.agent, label: Text('Agent')),
  ],
  selected: {_mode},
  onSelectionChanged: (value) => setState(() => _mode = value.single),
)
```

For management mode, show master URL, panel token, and profile name fields. On final submit call:

```dart
ref.read(authNotifierProvider.notifier).connectManagement(
  masterUrl: _urlController.text.trim(),
  panelToken: _tokenController.text.trim(),
  name: _nameController.text.trim(),
);
```

For agent mode, keep the existing register-token flow and call `register`.

- [ ] **Step 4: Add settings screen test**

Create `clients/flutter/test/features/settings/settings_screen_test.dart`:

```dart
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:nre_client/features/auth/data/models/auth_models.dart';
import 'package:nre_client/features/auth/presentation/providers/auth_provider.dart';
import 'package:nre_client/features/settings/presentation/screens/settings_screen.dart';
import 'package:nre_client/l10n/app_localizations.dart';

void main() {
  testWidgets('settings shows management and agent profiles separately', (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [authNotifierProvider.overrideWith(() => _AuthDouble())],
        child: const MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: SettingsScreen(),
        ),
      ),
    );

    await tester.pumpAndSettle();

    expect(find.text('Management profile'), findsOneWidget);
    expect(find.text('Agent profile'), findsOneWidget);
    expect(find.text('https://panel.example.com'), findsWidgets);
  });
}

class _AuthDouble extends AuthNotifier {
  @override
  Future<AuthState> build() async => const AuthStateAuthenticated(
        ClientProfile(
          masterUrl: 'https://panel.example.com',
          activeMode: ConnectionMode.management,
          management: ManagementProfile(panelToken: 'panel-secret'),
          agent: AgentProfile(agentId: 'agent-1', agentToken: 'agent-secret'),
        ),
      );
}
```

- [ ] **Step 5: Implement Settings profile sections**

In `settings_screen.dart`, replace the single connection card with:

- Active mode row.
- Management profile row with configured/not configured state and clear button.
- Agent profile row with agent id and clear-agent button.
- Existing theme/accent and about sections.

Use existing `GlassCard` and `GlassButton` components.

- [ ] **Step 6: Regenerate localization files**

If new strings are added to ARB files, run:

```powershell
cd clients/flutter
flutter gen-l10n
```

Expected: `app_localizations*.dart` update without errors.

- [ ] **Step 7: Verify screen tests**

Run:

```powershell
cd clients/flutter
flutter test test/features/auth/connect_screen_test.dart test/features/settings/settings_screen_test.dart
```

Expected: PASS.

- [ ] **Step 8: Commit connect/settings work**

Run:

```powershell
git add clients/flutter/lib/features/auth/presentation/screens/connect_screen.dart clients/flutter/lib/features/settings/presentation/screens/settings_screen.dart clients/flutter/lib/l10n clients/flutter/test/features/auth/connect_screen_test.dart clients/flutter/test/features/settings/settings_screen_test.dart
git commit -m "feat(flutter): expose management and agent connection modes"
```

---

## Task 5: CRUD Screens for Rules, Agents, Certificates, and Relay

**Files:**
- Modify: `clients/flutter/lib/features/rules/presentation/screens/rules_list_screen.dart`
- Modify: `clients/flutter/lib/features/rules/presentation/screens/rule_form_dialog.dart`
- Modify: `clients/flutter/lib/features/agents/presentation/screens/agents_screen.dart`
- Modify: `clients/flutter/lib/features/certificates/presentation/screens/certificates_screen.dart`
- Modify: `clients/flutter/lib/features/relay/presentation/screens/relay_screen.dart`
- Modify: `clients/flutter/lib/core/network/panel_api_client.dart`
- Test: `clients/flutter/test/features/rules/rule_form_dialog_test.dart`
- Test: `clients/flutter/test/features/agents/agents_screen_test.dart`
- Test: `clients/flutter/test/features/relay/relay_screen_test.dart`
- Test: `clients/flutter/test/features/certificates/certificates_screen_test.dart`

- [ ] **Step 1: Write failing rule form test**

Create `clients/flutter/test/features/rules/rule_form_dialog_test.dart`:

```dart
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:nre_client/features/rules/presentation/screens/rule_form_dialog.dart';
import 'package:nre_client/l10n/app_localizations.dart';

void main() {
  testWidgets('rule form includes frontend url and backend url fields', (tester) async {
    await tester.pumpWidget(
      const ProviderScope(
        child: MaterialApp(
          localizationsDelegates: AppLocalizations.localizationsDelegates,
          supportedLocales: AppLocalizations.supportedLocales,
          home: Scaffold(body: _OpenDialog()),
        ),
      ),
    );

    await tester.tap(find.text('Open'));
    await tester.pumpAndSettle();

    expect(find.text('Frontend URL'), findsOneWidget);
    expect(find.text('Backend URL'), findsOneWidget);
    expect(find.text('Enabled'), findsOneWidget);
  });
}

class _OpenDialog extends StatelessWidget {
  const _OpenDialog();

  @override
  Widget build(BuildContext context) {
    return TextButton(
      onPressed: () => showRuleFormDialog(context),
      child: const Text('Open'),
    );
  }
}
```

- [ ] **Step 2: Run rule form test and verify RED**

Run:

```powershell
cd clients/flutter
flutter test test/features/rules/rule_form_dialog_test.dart
```

Expected: FAIL because the current form still uses the old thin `domain/target/type` model.

- [ ] **Step 3: Implement HTTP rule form against typed request**

Update `rule_form_dialog.dart` to collect:

- Frontend URL.
- One backend URL.
- Enabled switch.
- Tags as comma-separated text.
- Proxy redirect switch.
- Pass proxy headers switch.
- Optional user agent.

On submit, construct:

```dart
CreateHttpRuleRequest(
  frontendUrl: frontendController.text.trim(),
  backends: [HttpBackend(url: backendController.text.trim())],
  enabled: enabled,
  tags: tagsController.text
      .split(',')
      .map((tag) => tag.trim())
      .where((tag) => tag.isNotEmpty)
      .toList(),
  proxyRedirect: proxyRedirect,
  passProxyHeaders: passProxyHeaders,
  userAgent: userAgentController.text.trim(),
)
```

For edit mode, construct `UpdateHttpRuleRequest.fromRule(...)` from edited values.

- [ ] **Step 4: Update rules list display and actions**

In `rules_list_screen.dart`:

- Display `rule.frontendUrl`.
- Display `rule.backendUrl`.
- Search against frontend/backend URLs.
- Toggle calls `rulesListProvider.notifier.toggleRule`.
- Delete keeps confirmation and rollback.
- Copy copies `${rule.frontendUrl} -> ${rule.backendUrl}`.

- [ ] **Step 5: Verify PanelApiClient mutation methods are covered**

Before wiring screen buttons, ensure `clients/flutter/test/core/network/panel_api_client_test.dart` contains passing tests for these methods and endpoint paths:

```dart
Future<AgentSummary> renameAgent(String agentId, String name)
Future<void> deleteAgent(String agentId)
Future<void> deleteCertificate(String agentId, String id)
Future<Certificate> issueCertificate(String agentId, String id)
Future<RelayListener> createRelayListener(String agentId, CreateRelayListenerRequest request)
Future<RelayListener> updateRelayListener(String agentId, String id, UpdateRelayListenerRequest request)
Future<void> deleteRelayListener(String agentId, String id)
```

Expected endpoint paths:

```text
PATCH  /panel-api/agents/{agentId}
DELETE /panel-api/agents/{agentId}
DELETE /panel-api/agents/{agentId}/certificates/{id}
POST   /panel-api/agents/{agentId}/certificates/{id}/issue
POST   /panel-api/agents/{agentId}/relay-listeners
PUT    /panel-api/agents/{agentId}/relay-listeners/{id}
DELETE /panel-api/agents/{agentId}/relay-listeners/{id}
```

- [ ] **Step 6: Replace remote agents empty state**

In `agents_screen.dart`:

- Keep local agent card.
- Read `agentsListProvider`.
- Render remote agent cards/table with name, status, platform/version, mode, revision state, last seen.
- Add search filter.
- Add menu actions: rename, apply config, delete.
- Keep existing local agent widget tests passing.

- [ ] **Step 7: Complete certificate actions**

In `certificates_screen.dart`:

- Import/request buttons open typed dialog instead of no-op.
- Details button opens a details dialog.
- Renew button calls `issueCertificate`.
- Delete action uses confirmation.
- Status filter continues to work with extended `Certificate`.

- [ ] **Step 8: Complete relay actions**

In `relay_screen.dart`:

- Add New button to filter bar.
- Edit menu opens relay form dialog.
- Form captures name, listen port, bind hosts, enabled, certificate source, trust mode fields.
- Toggle and delete use provider rollback pattern.

- [ ] **Step 9: Verify CRUD widget tests**

Run:

```powershell
cd clients/flutter
flutter test test/features/rules/rule_form_dialog_test.dart test/features/agents/agents_screen_test.dart test/features/relay/relay_screen_test.dart test/features/certificates/certificates_screen_test.dart
```

Expected: PASS.

- [ ] **Step 10: Commit CRUD screen work**

Run:

```powershell
git add clients/flutter/lib clients/flutter/test/features
git commit -m "feat(flutter): complete daily crud screens"
```

---

## Task 6: Dashboard Uses Real Provider Data

**Files:**
- Modify: `clients/flutter/lib/features/dashboard/presentation/providers/dashboard_provider.dart`
- Modify: `clients/flutter/lib/features/dashboard/presentation/screens/dashboard_screen.dart`
- Test: `clients/flutter/test/features/dashboard/dashboard_provider_test.dart`
- Test: `clients/flutter/test/features/dashboard/dashboard_screen_test.dart`

- [ ] **Step 1: Write failing dashboard provider test**

Create `clients/flutter/test/features/dashboard/dashboard_provider_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/features/dashboard/presentation/providers/dashboard_provider.dart';

void main() {
  test('DashboardSummary counts disabled rules and active relays', () {
    final summary = DashboardSummary(
      rulesTotal: 3,
      rulesDisabled: 1,
      agentsTotal: 2,
      agentsOnline: 1,
      certificatesTotal: 4,
      certificatesExpiring: 2,
      relaysTotal: 5,
      relaysActive: 3,
    );

    expect(summary.rulesActive, 2);
    expect(summary.agentsOffline, 1);
  });
}
```

- [ ] **Step 2: Run dashboard provider test and verify RED**

Run:

```powershell
cd clients/flutter
flutter test test/features/dashboard/dashboard_provider_test.dart
```

Expected: FAIL because `DashboardSummary` does not exist.

- [ ] **Step 3: Implement DashboardSummary and provider**

Create/update `dashboard_provider.dart`:

```dart
class DashboardSummary {
  const DashboardSummary({
    required this.rulesTotal,
    required this.rulesDisabled,
    required this.agentsTotal,
    required this.agentsOnline,
    required this.certificatesTotal,
    required this.certificatesExpiring,
    required this.relaysTotal,
    required this.relaysActive,
  });

  final int rulesTotal;
  final int rulesDisabled;
  final int agentsTotal;
  final int agentsOnline;
  final int certificatesTotal;
  final int certificatesExpiring;
  final int relaysTotal;
  final int relaysActive;

  int get rulesActive => rulesTotal - rulesDisabled;
  int get agentsOffline => agentsTotal - agentsOnline;
}
```

Add a Riverpod provider that reads current rules/agents/certificates/relay async values and returns the summary.

- [ ] **Step 4: Update DashboardScreen stat cards**

Replace hard-coded certificate and relay `—` values with `DashboardSummary` values. Keep local agent runtime card and quick actions.

- [ ] **Step 5: Verify dashboard tests**

Run:

```powershell
cd clients/flutter
flutter test test/features/dashboard/dashboard_provider_test.dart
```

Expected: PASS.

- [ ] **Step 6: Commit dashboard work**

Run:

```powershell
git add clients/flutter/lib/features/dashboard clients/flutter/test/features/dashboard
git commit -m "feat(flutter): show real dashboard summary"
```

---

## Task 7: Final Verification and Cleanup

**Files:**
- Modify only files needed to fix compile, formatting, localization, or test failures from prior tasks.
- Update: `clients/flutter/README.md` if connection modes need user-facing development notes.

- [ ] **Step 1: Run code generation**

Run:

```powershell
cd clients/flutter
dart run build_runner build --delete-conflicting-outputs
flutter gen-l10n
```

Expected: generated files are current and commands complete without errors.

- [ ] **Step 2: Run Flutter tests**

Run:

```powershell
cd clients/flutter
flutter test
```

Expected: all Flutter tests pass.

- [ ] **Step 3: Run Flutter analyzer**

Run:

```powershell
cd clients/flutter
flutter analyze
```

Expected: no errors. Existing warnings may be fixed if they are in files touched by this work.

- [ ] **Step 4: Run backend tests only if backend contracts changed**

If implementation changed Go backend API behavior, run:

```powershell
cd panel/backend-go
go test ./...
```

Expected: all Go control-plane tests pass.

- [ ] **Step 5: Inspect git diff**

Run:

```powershell
git status --short
git diff --stat
```

Expected: only Flutter client, generated Flutter files, and optional Flutter README changes are present.

- [ ] **Step 6: Commit final cleanup**

Run:

```powershell
git add clients/flutter
git commit -m "test(flutter): verify daily crud client milestone"
```

Skip this commit if there are no changes after previous task commits.
