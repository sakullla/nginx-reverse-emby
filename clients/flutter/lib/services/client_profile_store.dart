import 'dart:convert';
import 'dart:io';

import 'package:path_provider/path_provider.dart';

import '../core/client_state.dart';

abstract class ClientProfileStore {
  Future<ClientProfile> load();

  Future<void> save(ClientProfile profile);
}

class FileClientProfileStore implements ClientProfileStore {
  FileClientProfileStore({required String baseDir})
    : profilePath = '$baseDir${Platform.pathSeparator}profile.json';

  final String profilePath;

  @override
  Future<ClientProfile> load() async {
    try {
      final file = File(profilePath);
      if (!await file.exists()) {
        return ClientState.empty().profile;
      }
      final decoded = jsonDecode(await file.readAsString());
      if (decoded is! Map<String, Object?>) {
        return ClientState.empty().profile;
      }
      return ClientProfile.fromJson(decoded);
    } catch (_) {
      return ClientState.empty().profile;
    }
  }

  @override
  Future<void> save(ClientProfile profile) async {
    final file = File(profilePath);
    await file.parent.create(recursive: true);
    await file.writeAsString(jsonEncode(profile.toJson()));
  }
}

class PathProviderClientProfileStore implements ClientProfileStore {
  FileClientProfileStore? _delegate;

  Future<FileClientProfileStore> _store() async {
    final existing = _delegate;
    if (existing != null) {
      return existing;
    }
    final dir = await getApplicationSupportDirectory();
    return _delegate = FileClientProfileStore(baseDir: dir.path);
  }

  @override
  Future<ClientProfile> load() async {
    try {
      return await (await _store()).load();
    } catch (_) {
      return ClientState.empty().profile;
    }
  }

  @override
  Future<void> save(ClientProfile profile) async {
    await (await _store()).save(profile);
  }
}
