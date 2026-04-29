import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:nre_client/core/client_state.dart';
import 'package:nre_client/services/client_profile_store.dart';

void main() {
  late Directory tempDir;
  late FileClientProfileStore store;

  setUp(() async {
    tempDir = await Directory.systemTemp.createTemp('nre-profile-store-test-');
    store = FileClientProfileStore(baseDir: tempDir.path);
  });

  tearDown(() async {
    if (await tempDir.exists()) {
      await tempDir.delete(recursive: true);
    }
  });

  test('load returns empty profile when profile file is missing', () async {
    final profile = await store.load();

    expect(profile.isRegistered, isFalse);
    expect(profile.masterUrl, '');
  });

  test('save persists profile and load restores it', () async {
    const profile = ClientProfile(
      masterUrl: 'https://panel.example.com',
      displayName: 'windows-test',
      agentId: 'agent-1',
      token: 'agent-secret',
    );

    await store.save(profile);
    final restored = await store.load();

    expect(restored.masterUrl, profile.masterUrl);
    expect(restored.displayName, profile.displayName);
    expect(restored.agentId, profile.agentId);
    expect(restored.token, profile.token);
  });

  test('load returns empty profile when profile file is invalid', () async {
    await File(store.profilePath).create(recursive: true);
    await File(store.profilePath).writeAsString('{not json');

    final profile = await store.load();

    expect(profile.isRegistered, isFalse);
    expect(profile.masterUrl, '');
  });
}
