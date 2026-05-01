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
  String get navCertificates => 'Certificates';

  @override
  String get navRelay => 'Relay';

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
  String get statusActive => 'Active';

  @override
  String get statusDisabled => 'Disabled';

  @override
  String get statusNotConnected => 'Not Connected';

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
  String get labelDomain => 'Domain';

  @override
  String get hintMasterUrl => 'https://your-server.com';

  @override
  String get hintRegisterToken => 'Enter token from master server';

  @override
  String get hintClientName => 'nre-client';

  @override
  String get hintSearchRules => 'Search rules...';

  @override
  String get hintSearchRelays => 'Search relays...';

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
  String get errorEnterUrl => 'Please enter URL';

  @override
  String get errorEnterToken => 'Please enter Token';

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
  String get btnImport => 'Import';

  @override
  String get btnRequest => 'Request';

  @override
  String get btnRenew => 'Renew';

  @override
  String get btnDetails => 'Details';

  @override
  String get btnSave => 'Save';

  @override
  String get btnSaving => 'Saving...';

  @override
  String get btnDelete => 'Delete';

  @override
  String get btnConnect => 'Connect';

  @override
  String get btnDisconnect => 'Disconnect';

  @override
  String get btnPrevious => 'Previous';

  @override
  String get btnNext => 'Next';

  @override
  String get btnNew => '+ New';

  @override
  String get btnCreateRule => '+ Create Rule';

  @override
  String get btnViewLogs => 'View Logs';

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
  String get titleAppearance => 'Appearance';

  @override
  String get titleThemeMode => 'Theme Mode';

  @override
  String get titleAccentColor => 'Accent Color';

  @override
  String get titleConnectToMaster => 'Connect to Master';

  @override
  String get titleNewRule => 'New Rule';

  @override
  String get titleEditRule => 'Edit Rule';

  @override
  String get titleDeleteRule => 'Delete Rule';

  @override
  String get titleDeleteRelay => 'Delete Relay Listener';

  @override
  String get titleNoCertificates => 'No Certificates';

  @override
  String get titleNoRemoteAgents => 'No remote agents';

  @override
  String get titleNoRelayListeners => 'No Relay Listeners';

  @override
  String get titleQuickActions => 'Quick Actions';

  @override
  String get titleRemoteAgents => 'Remote Agents';

  @override
  String get titleSelfSigned => 'Self-signed';

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
  String get descPleaseConnectFirst =>
      'Please connect to a Master server first';

  @override
  String get descCreateFirstRule =>
      'Create your first proxy rule to get started';

  @override
  String get descImportOrRequestCert =>
      'Import or request SSL certificates to get started';

  @override
  String get descRemoteAgentsAppearHere =>
      'Remote agents that register with this master will appear here.';

  @override
  String get descRelayListenersAppearHere =>
      'Relay listeners will appear here once configured';

  @override
  String descDeleteRuleConfirm(String domain) {
    return 'Are you sure you want to delete \"$domain\"? This action cannot be undone.';
  }

  @override
  String descDeleteRelayConfirm(String address, String protocol) {
    return 'Are you sure you want to delete \"$address\" ($protocol)? This action cannot be undone.';
  }

  @override
  String get descSystemRunningNormal => 'System running normally';

  @override
  String get descAllAgentsOnlineLastSync =>
      'All agents online · Last sync: 30s ago';

  @override
  String get descNotRunning => 'Not Running';

  @override
  String get descNotAvailable => 'Not Available';

  @override
  String get descNoAgentAssigned => 'No agent assigned';

  @override
  String get descNotConnectedMaster => 'Not connected';

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
  String get msgRuleCopiedToClipboard => 'Rule copied to clipboard';

  @override
  String msgFailedToSaveRule(String error) {
    return 'Failed to save rule: $error';
  }

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
  String get valueAppVersion => 'v2.1.0';

  @override
  String get titleAgentProcessControl => 'Agent Process Control';

  @override
  String get labelTheme => 'Theme';

  @override
  String get valueThemeSystem => 'System';

  @override
  String get valueThemeLight => 'Light';

  @override
  String get valueThemeDark => 'Dark';

  @override
  String get trayShow => 'Show';

  @override
  String get trayQuit => 'Quit';

  @override
  String get filterStatus => 'Status';

  @override
  String get filterType => 'Type';

  @override
  String get filterAllStatus => 'All Status';

  @override
  String get filterAllProtocols => 'All Protocols';

  @override
  String get certStatusValid => 'Valid';

  @override
  String get certStatusExpiring => 'Expiring';

  @override
  String get certStatusExpired => 'Expired';

  @override
  String get labelOverdue => 'overdue';

  @override
  String get labelRemaining => 'remaining';

  @override
  String labelIssued(String date) {
    return 'Issued: $date';
  }

  @override
  String get labelUsedBy => 'Used by:';

  @override
  String labelAgent(String name) {
    return 'Agent: $name';
  }

  @override
  String labelCertificateCount(int count, String plural) {
    return '$count certificate$plural';
  }

  @override
  String labelRelayCount(int count, String plural) {
    return '$count relay$plural';
  }

  @override
  String labelRegisteredCount(int count) {
    return '$count registered';
  }

  @override
  String labelDisabledCount(int count) {
    return '$count disabled';
  }

  @override
  String get labelAllOnline => 'All online';

  @override
  String labelOffline(int count) {
    return '$count offline';
  }

  @override
  String labelExpiringWarning(int count, String plural) {
    return '$count certificate$plural expiring within 14 days';
  }

  @override
  String get labelReview => 'Review →';

  @override
  String get stepServerUrl => 'Server URL';

  @override
  String get stepRegisterToken => 'Register Token';

  @override
  String get stepClientName => 'Client Name';

  @override
  String get actionNewRule => 'New Rule';

  @override
  String get actionAddCertificate => 'Add Certificate';

  @override
  String get actionAddAgent => 'Add Agent';

  @override
  String get actionNewRelay => 'New Relay';

  @override
  String get metaUptime => 'Uptime';

  @override
  String get metaVersion => 'Version';

  @override
  String get metaLastSync => 'Last sync';

  @override
  String get metaSync30sAgo => '30s ago';

  @override
  String get failedToLoadDashboard => 'Failed to load dashboard';

  @override
  String get failedToLoadRules => 'Failed to load rules';

  @override
  String get failedToLoadCertificates => 'Failed to load certificates';

  @override
  String get failedToLoadRelays => 'Failed to load relay listeners';

  @override
  String get navMore => 'More';
}
