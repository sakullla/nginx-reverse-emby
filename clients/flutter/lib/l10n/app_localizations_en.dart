// ignore: unused_import
import 'package:intl/intl.dart' as intl;
import 'app_localizations.dart';

// ignore_for_file: type=lint

/// The translations for English (`en`).
class AppLocalizationsEn extends AppLocalizations {
  AppLocalizationsEn([String locale = 'en']) : super(locale);

  @override
  String get appTitle => 'NRE Client';

  @override
  String get navDashboard => 'Dashboard';

  @override
  String get navAgent => 'Agent';

  @override
  String get navRules => 'Rules';

  @override
  String get navSettings => 'Settings';

  @override
  String get statusRegistered => 'Registered';

  @override
  String get statusNotRegistered => 'Not registered';

  @override
  String get statusRunning => 'Running';

  @override
  String get statusStopped => 'Stopped';

  @override
  String get statusUnavailable => 'Unavailable';

  @override
  String get statusUnknown => 'Unknown';

  @override
  String get statusChecking => 'Checking';

  @override
  String get labelMasterUrl => 'Master URL';

  @override
  String get labelAgentId => 'Agent ID';

  @override
  String get labelDisplayName => 'Display Name';

  @override
  String get labelToken => 'Token';

  @override
  String get labelClientName => 'Client name';

  @override
  String get labelRegisterToken => 'Register token';

  @override
  String get labelPid => 'PID';

  @override
  String get labelBinaryPath => 'Binary Path';

  @override
  String get labelDataDir => 'Data Directory';

  @override
  String get labelLogPath => 'Log Path';

  @override
  String get labelMessage => 'Message';

  @override
  String get labelPlatform => 'Platform';

  @override
  String get labelAgentStatus => 'Agent Status';

  @override
  String get labelType => 'Type';

  @override
  String get labelTarget => 'Target';

  @override
  String get labelEnabled => 'Enabled';

  @override
  String get labelDisabled => 'Disabled';

  @override
  String get labelNotConfigured => 'Not configured';

  @override
  String get labelNotRegistered => 'Not registered';

  @override
  String get hintMasterUrl => 'https://your-server.com';

  @override
  String get hintRegisterToken => 'Enter token from master server';

  @override
  String get hintClientName => 'nre-client';

  @override
  String get errorRequiredMasterUrl => 'Master URL is required';

  @override
  String get errorRequiredRegisterToken => 'Register token is required';

  @override
  String errorRegistrationFailed(String error) {
    return 'Registration failed: $error';
  }

  @override
  String get errorMasterUrlScheme => 'Master URL must use http or https';

  @override
  String get errorMasterUrlHost => 'Master URL must include a host';

  @override
  String get errorNoAgentId =>
      'Registration response did not include an agent id';

  @override
  String errorInvalidResponse(String message) {
    return 'Invalid backend response: $message';
  }

  @override
  String get btnRegister => 'Register';

  @override
  String get btnUnregister => 'Unregister';

  @override
  String get btnCancel => 'Cancel';

  @override
  String get btnClear => 'Clear';

  @override
  String get btnCopy => 'Copy';

  @override
  String get btnCopyId => 'Copy ID';

  @override
  String get btnStart => 'Start';

  @override
  String get btnStop => 'Stop';

  @override
  String get btnRestart => 'Restart';

  @override
  String get btnRefresh => 'Refresh';

  @override
  String get btnRetry => 'Retry';

  @override
  String get btnViewDetails => 'View Details';

  @override
  String get btnRegisterNow => 'Register Now';

  @override
  String get titleRegisterAgent => 'Register Agent';

  @override
  String get titleAgent => 'Agent';

  @override
  String get titleDashboard => 'Dashboard';

  @override
  String get titleRules => 'Rules';

  @override
  String get titleSettings => 'Settings';

  @override
  String get titleControl => 'Control';

  @override
  String get titleLogs => 'Logs';

  @override
  String get titleConnection => 'Connection';

  @override
  String get titleLocalAgent => 'Local Agent';

  @override
  String get titleOverview => 'Overview';

  @override
  String get titleAgentLogs => 'Agent Logs';

  @override
  String get titleRegistration => 'Registration';

  @override
  String get titleLocalAgentProcess => 'Local Agent Process';

  @override
  String get titleUnregisterAgent => 'Unregister Agent';

  @override
  String get titleClearLogs => 'Clear Logs';

  @override
  String get titleClearAllData => 'Clear All Data';

  @override
  String get titleNotConnected => 'Not Connected';

  @override
  String get titleError => 'Error';

  @override
  String get titleNoRules => 'No Rules';

  @override
  String get titleLocalStorage => 'Local Storage';

  @override
  String get titleSystem => 'System';

  @override
  String get titleAbout => 'About';

  @override
  String get titleExportProfile => 'Export Profile';

  @override
  String get titleStartAtLogin => 'Start at Login';

  @override
  String get descRegisterAgent =>
      'Connect this client to a master server. You will need a register token from the server.';

  @override
  String get descUnregisterConfirm =>
      'This will remove the local registration. The agent on the master server will need to be re-registered.';

  @override
  String get descClearLogs =>
      'This only clears the displayed logs. The log file on disk is not affected.';

  @override
  String get descClearAllData =>
      'This will erase all local data including your registration profile. The agent on the master server will remain but this client will need to be re-registered.';

  @override
  String get descNotConnected =>
      'Register your agent on the Agent page to view rules from the master server.';

  @override
  String get descNoRules =>
      'No proxy rules are configured on the master server.';

  @override
  String get descRegisterClient =>
      'Register this client to connect to a master server.';

  @override
  String get descUnableDetermineStatus => 'Unable to determine agent status';

  @override
  String get descExportProfile => 'Copy profile JSON to clipboard';

  @override
  String get descClearData => 'Remove registration and local cache';

  @override
  String get descStartAtLogin => 'Launch client when system starts';

  @override
  String msgRegistered(String agentId) {
    return 'Registered agent $agentId';
  }

  @override
  String get msgUnregistered => 'Unregistered';

  @override
  String msgAgentStarted(String pid) {
    return 'Agent started (PID: $pid)';
  }

  @override
  String get msgAgentStopped => 'Agent stopped';

  @override
  String msgAgentAction(String action) {
    return 'Agent $action';
  }

  @override
  String msgActionFailed(String error) {
    return 'Failed: $error';
  }

  @override
  String get msgCopied => 'Copied';

  @override
  String get msgCopiedToClipboard => 'Copied to clipboard';

  @override
  String get msgProfileExported => 'Profile JSON copied to clipboard';

  @override
  String get msgNoProfileToExport => 'No registered profile to export';

  @override
  String get msgAllDataCleared => 'All local data cleared';

  @override
  String get msgStartAtLoginEnabled => 'Start at login enabled (placeholder)';

  @override
  String get msgStartAtLoginDisabled => 'Start at login disabled (placeholder)';

  @override
  String msgLastUpdated(String time) {
    return 'Last updated: $time';
  }

  @override
  String get msgNoLogs => 'No logs available.\nStart the agent to see logs.';

  @override
  String get msgLogsCleared => 'Logs view cleared';

  @override
  String get labelApplication => 'Application';

  @override
  String get labelVersion => 'Version';

  @override
  String get labelDistribution => 'Distribution';

  @override
  String get labelContainerPolicy => 'Container Policy';

  @override
  String get valueAppName => 'NRE Client';

  @override
  String get valueGithubRelease => 'GitHub Release';

  @override
  String get valueContainerPolicyDesc =>
      'Client artifacts are not embedded in the control-plane image';

  @override
  String get valueLoading => 'Loading...';

  @override
  String get valueDash => '-';

  @override
  String get titleAgentProcessControl => 'Agent Process Control';
}
