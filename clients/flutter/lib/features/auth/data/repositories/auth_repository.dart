import 'dart:convert';
import 'package:shared_preferences/shared_preferences.dart';
import '../models/auth_models.dart';

class AuthRepository {
  static const _profileKey = 'client_profile';

  Future<ClientProfile> loadProfile() async {
    final prefs = await SharedPreferences.getInstance();
    final json = prefs.getString(_profileKey);
    if (json == null) return const ClientProfile();
    try {
      return ClientProfile.fromJson(
        jsonDecode(json) as Map<String, dynamic>,
      );
    } catch (_) {
      return const ClientProfile();
    }
  }

  Future<void> saveProfile(ClientProfile profile) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_profileKey, jsonEncode(profile.toJson()));
  }

  Future<void> clearProfile() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_profileKey);
  }
}
