import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import 'package:intl/intl.dart' as intl;

import 'app_localizations_en.dart';
import 'app_localizations_zh.dart';

// ignore_for_file: type=lint

/// Callers can lookup localized strings with an instance of AppLocalizations
/// returned by `AppLocalizations.of(context)`.
///
/// Applications need to include `AppLocalizations.delegate()` in their app's
/// `localizationDelegates` list, and the locales they support in the app's
/// `supportedLocales` list. For example:
///
/// ```dart
/// import 'l10n/app_localizations.dart';
///
/// return MaterialApp(
///   localizationsDelegates: AppLocalizations.localizationsDelegates,
///   supportedLocales: AppLocalizations.supportedLocales,
///   home: MyApplicationHome(),
/// );
/// ```
///
/// ## Update pubspec.yaml
///
/// Please make sure to update your pubspec.yaml to include the following
/// packages:
///
/// ```yaml
/// dependencies:
///   # Internationalization support.
///   flutter_localizations:
///     sdk: flutter
///   intl: any # Use the pinned version from flutter_localizations
///
///   # Rest of dependencies
/// ```
///
/// ## iOS Applications
///
/// iOS applications define key application metadata, including supported
/// locales, in an Info.plist file that is built into the application bundle.
/// To configure the locales supported by your app, you’ll need to edit this
/// file.
///
/// First, open your project’s ios/Runner.xcworkspace Xcode workspace file.
/// Then, in the Project Navigator, open the Info.plist file under the Runner
/// project’s Runner folder.
///
/// Next, select the Information Property List item, select Add Item from the
/// Editor menu, then select Localizations from the pop-up menu.
///
/// Select and expand the newly-created Localizations item then, for each
/// locale your application supports, add a new item and select the locale
/// you wish to add from the pop-up menu in the Value field. This list should
/// be consistent with the languages listed in the AppLocalizations.supportedLocales
/// property.
abstract class AppLocalizations {
  AppLocalizations(String locale)
    : localeName = intl.Intl.canonicalizedLocale(locale.toString());

  final String localeName;

  static AppLocalizations? of(BuildContext context) {
    return Localizations.of<AppLocalizations>(context, AppLocalizations);
  }

  static const LocalizationsDelegate<AppLocalizations> delegate =
      _AppLocalizationsDelegate();

  /// A list of this localizations delegate along with the default localizations
  /// delegates.
  ///
  /// Returns a list of localizations delegates containing this delegate along with
  /// GlobalMaterialLocalizations.delegate, GlobalCupertinoLocalizations.delegate,
  /// and GlobalWidgetsLocalizations.delegate.
  ///
  /// Additional delegates can be added by appending to this list in
  /// MaterialApp. This list does not have to be used at all if a custom list
  /// of delegates is preferred or required.
  static const List<LocalizationsDelegate<dynamic>> localizationsDelegates =
      <LocalizationsDelegate<dynamic>>[
        delegate,
        GlobalMaterialLocalizations.delegate,
        GlobalCupertinoLocalizations.delegate,
        GlobalWidgetsLocalizations.delegate,
      ];

  /// A list of this localizations delegate's supported locales.
  static const List<Locale> supportedLocales = <Locale>[
    Locale('zh'),
    Locale('en'),
  ];

  /// No description provided for @appTitle.
  ///
  /// In en, this message translates to:
  /// **'NRE Client'**
  String get appTitle;

  /// No description provided for @navDashboard.
  ///
  /// In en, this message translates to:
  /// **'Dashboard'**
  String get navDashboard;

  /// No description provided for @navAgent.
  ///
  /// In en, this message translates to:
  /// **'Agent'**
  String get navAgent;

  /// No description provided for @navRules.
  ///
  /// In en, this message translates to:
  /// **'Rules'**
  String get navRules;

  /// No description provided for @navSettings.
  ///
  /// In en, this message translates to:
  /// **'Settings'**
  String get navSettings;

  /// No description provided for @navCertificates.
  ///
  /// In en, this message translates to:
  /// **'Certificates'**
  String get navCertificates;

  /// No description provided for @navRelay.
  ///
  /// In en, this message translates to:
  /// **'Relay'**
  String get navRelay;

  /// No description provided for @statusRegistered.
  ///
  /// In en, this message translates to:
  /// **'Registered'**
  String get statusRegistered;

  /// No description provided for @statusNotRegistered.
  ///
  /// In en, this message translates to:
  /// **'Not registered'**
  String get statusNotRegistered;

  /// No description provided for @statusRunning.
  ///
  /// In en, this message translates to:
  /// **'Running'**
  String get statusRunning;

  /// No description provided for @statusStopped.
  ///
  /// In en, this message translates to:
  /// **'Stopped'**
  String get statusStopped;

  /// No description provided for @statusUnavailable.
  ///
  /// In en, this message translates to:
  /// **'Unavailable'**
  String get statusUnavailable;

  /// No description provided for @statusUnknown.
  ///
  /// In en, this message translates to:
  /// **'Unknown'**
  String get statusUnknown;

  /// No description provided for @statusChecking.
  ///
  /// In en, this message translates to:
  /// **'Checking'**
  String get statusChecking;

  /// No description provided for @statusActive.
  ///
  /// In en, this message translates to:
  /// **'Active'**
  String get statusActive;

  /// No description provided for @statusDisabled.
  ///
  /// In en, this message translates to:
  /// **'Disabled'**
  String get statusDisabled;

  /// No description provided for @statusNotConnected.
  ///
  /// In en, this message translates to:
  /// **'Not Connected'**
  String get statusNotConnected;

  /// No description provided for @labelMasterUrl.
  ///
  /// In en, this message translates to:
  /// **'Master URL'**
  String get labelMasterUrl;

  /// No description provided for @labelAgentId.
  ///
  /// In en, this message translates to:
  /// **'Agent ID'**
  String get labelAgentId;

  /// No description provided for @labelDisplayName.
  ///
  /// In en, this message translates to:
  /// **'Display Name'**
  String get labelDisplayName;

  /// No description provided for @labelToken.
  ///
  /// In en, this message translates to:
  /// **'Token'**
  String get labelToken;

  /// No description provided for @labelClientName.
  ///
  /// In en, this message translates to:
  /// **'Client name'**
  String get labelClientName;

  /// No description provided for @labelRegisterToken.
  ///
  /// In en, this message translates to:
  /// **'Register token'**
  String get labelRegisterToken;

  /// No description provided for @labelPid.
  ///
  /// In en, this message translates to:
  /// **'PID'**
  String get labelPid;

  /// No description provided for @labelBinaryPath.
  ///
  /// In en, this message translates to:
  /// **'Binary Path'**
  String get labelBinaryPath;

  /// No description provided for @labelDataDir.
  ///
  /// In en, this message translates to:
  /// **'Data Directory'**
  String get labelDataDir;

  /// No description provided for @labelLogPath.
  ///
  /// In en, this message translates to:
  /// **'Log Path'**
  String get labelLogPath;

  /// No description provided for @labelMessage.
  ///
  /// In en, this message translates to:
  /// **'Message'**
  String get labelMessage;

  /// No description provided for @labelPlatform.
  ///
  /// In en, this message translates to:
  /// **'Platform'**
  String get labelPlatform;

  /// No description provided for @labelAgentStatus.
  ///
  /// In en, this message translates to:
  /// **'Agent Status'**
  String get labelAgentStatus;

  /// No description provided for @labelType.
  ///
  /// In en, this message translates to:
  /// **'Type'**
  String get labelType;

  /// No description provided for @labelTarget.
  ///
  /// In en, this message translates to:
  /// **'Target'**
  String get labelTarget;

  /// No description provided for @labelEnabled.
  ///
  /// In en, this message translates to:
  /// **'Enabled'**
  String get labelEnabled;

  /// No description provided for @labelDisabled.
  ///
  /// In en, this message translates to:
  /// **'Disabled'**
  String get labelDisabled;

  /// No description provided for @labelNotConfigured.
  ///
  /// In en, this message translates to:
  /// **'Not configured'**
  String get labelNotConfigured;

  /// No description provided for @labelNotRegistered.
  ///
  /// In en, this message translates to:
  /// **'Not registered'**
  String get labelNotRegistered;

  /// No description provided for @labelDomain.
  ///
  /// In en, this message translates to:
  /// **'Domain'**
  String get labelDomain;

  /// No description provided for @hintMasterUrl.
  ///
  /// In en, this message translates to:
  /// **'https://your-server.com'**
  String get hintMasterUrl;

  /// No description provided for @hintRegisterToken.
  ///
  /// In en, this message translates to:
  /// **'Enter token from master server'**
  String get hintRegisterToken;

  /// No description provided for @hintClientName.
  ///
  /// In en, this message translates to:
  /// **'nre-client'**
  String get hintClientName;

  /// No description provided for @hintSearchRules.
  ///
  /// In en, this message translates to:
  /// **'Search rules...'**
  String get hintSearchRules;

  /// No description provided for @hintSearchRelays.
  ///
  /// In en, this message translates to:
  /// **'Search relays...'**
  String get hintSearchRelays;

  /// No description provided for @errorRequiredMasterUrl.
  ///
  /// In en, this message translates to:
  /// **'Master URL is required'**
  String get errorRequiredMasterUrl;

  /// No description provided for @errorRequiredRegisterToken.
  ///
  /// In en, this message translates to:
  /// **'Register token is required'**
  String get errorRequiredRegisterToken;

  /// No description provided for @errorRegistrationFailed.
  ///
  /// In en, this message translates to:
  /// **'Registration failed: {error}'**
  String errorRegistrationFailed(String error);

  /// No description provided for @errorMasterUrlScheme.
  ///
  /// In en, this message translates to:
  /// **'Master URL must use http or https'**
  String get errorMasterUrlScheme;

  /// No description provided for @errorMasterUrlHost.
  ///
  /// In en, this message translates to:
  /// **'Master URL must include a host'**
  String get errorMasterUrlHost;

  /// No description provided for @errorNoAgentId.
  ///
  /// In en, this message translates to:
  /// **'Registration response did not include an agent id'**
  String get errorNoAgentId;

  /// No description provided for @errorInvalidResponse.
  ///
  /// In en, this message translates to:
  /// **'Invalid backend response: {message}'**
  String errorInvalidResponse(String message);

  /// No description provided for @errorEnterUrl.
  ///
  /// In en, this message translates to:
  /// **'Please enter URL'**
  String get errorEnterUrl;

  /// No description provided for @errorEnterToken.
  ///
  /// In en, this message translates to:
  /// **'Please enter Token'**
  String get errorEnterToken;

  /// No description provided for @btnRegister.
  ///
  /// In en, this message translates to:
  /// **'Register'**
  String get btnRegister;

  /// No description provided for @btnUnregister.
  ///
  /// In en, this message translates to:
  /// **'Unregister'**
  String get btnUnregister;

  /// No description provided for @btnCancel.
  ///
  /// In en, this message translates to:
  /// **'Cancel'**
  String get btnCancel;

  /// No description provided for @btnClear.
  ///
  /// In en, this message translates to:
  /// **'Clear'**
  String get btnClear;

  /// No description provided for @btnCopy.
  ///
  /// In en, this message translates to:
  /// **'Copy'**
  String get btnCopy;

  /// No description provided for @btnCopyId.
  ///
  /// In en, this message translates to:
  /// **'Copy ID'**
  String get btnCopyId;

  /// No description provided for @btnStart.
  ///
  /// In en, this message translates to:
  /// **'Start'**
  String get btnStart;

  /// No description provided for @btnStop.
  ///
  /// In en, this message translates to:
  /// **'Stop'**
  String get btnStop;

  /// No description provided for @btnRestart.
  ///
  /// In en, this message translates to:
  /// **'Restart'**
  String get btnRestart;

  /// No description provided for @btnRefresh.
  ///
  /// In en, this message translates to:
  /// **'Refresh'**
  String get btnRefresh;

  /// No description provided for @btnRetry.
  ///
  /// In en, this message translates to:
  /// **'Retry'**
  String get btnRetry;

  /// No description provided for @btnViewDetails.
  ///
  /// In en, this message translates to:
  /// **'View Details'**
  String get btnViewDetails;

  /// No description provided for @btnRegisterNow.
  ///
  /// In en, this message translates to:
  /// **'Register Now'**
  String get btnRegisterNow;

  /// No description provided for @btnImport.
  ///
  /// In en, this message translates to:
  /// **'Import'**
  String get btnImport;

  /// No description provided for @btnRequest.
  ///
  /// In en, this message translates to:
  /// **'Request'**
  String get btnRequest;

  /// No description provided for @btnRenew.
  ///
  /// In en, this message translates to:
  /// **'Renew'**
  String get btnRenew;

  /// No description provided for @btnDetails.
  ///
  /// In en, this message translates to:
  /// **'Details'**
  String get btnDetails;

  /// No description provided for @btnSave.
  ///
  /// In en, this message translates to:
  /// **'Save'**
  String get btnSave;

  /// No description provided for @btnSaving.
  ///
  /// In en, this message translates to:
  /// **'Saving...'**
  String get btnSaving;

  /// No description provided for @btnDelete.
  ///
  /// In en, this message translates to:
  /// **'Delete'**
  String get btnDelete;

  /// No description provided for @btnConnect.
  ///
  /// In en, this message translates to:
  /// **'Connect'**
  String get btnConnect;

  /// No description provided for @btnDisconnect.
  ///
  /// In en, this message translates to:
  /// **'Disconnect'**
  String get btnDisconnect;

  /// No description provided for @btnPrevious.
  ///
  /// In en, this message translates to:
  /// **'Previous'**
  String get btnPrevious;

  /// No description provided for @btnNext.
  ///
  /// In en, this message translates to:
  /// **'Next'**
  String get btnNext;

  /// No description provided for @btnNew.
  ///
  /// In en, this message translates to:
  /// **'+ New'**
  String get btnNew;

  /// No description provided for @btnCreateRule.
  ///
  /// In en, this message translates to:
  /// **'+ Create Rule'**
  String get btnCreateRule;

  /// No description provided for @btnViewLogs.
  ///
  /// In en, this message translates to:
  /// **'View Logs'**
  String get btnViewLogs;

  /// No description provided for @titleRegisterAgent.
  ///
  /// In en, this message translates to:
  /// **'Register Agent'**
  String get titleRegisterAgent;

  /// No description provided for @titleAgent.
  ///
  /// In en, this message translates to:
  /// **'Agent'**
  String get titleAgent;

  /// No description provided for @titleDashboard.
  ///
  /// In en, this message translates to:
  /// **'Dashboard'**
  String get titleDashboard;

  /// No description provided for @titleRules.
  ///
  /// In en, this message translates to:
  /// **'Rules'**
  String get titleRules;

  /// No description provided for @titleSettings.
  ///
  /// In en, this message translates to:
  /// **'Settings'**
  String get titleSettings;

  /// No description provided for @titleControl.
  ///
  /// In en, this message translates to:
  /// **'Control'**
  String get titleControl;

  /// No description provided for @titleLogs.
  ///
  /// In en, this message translates to:
  /// **'Logs'**
  String get titleLogs;

  /// No description provided for @titleConnection.
  ///
  /// In en, this message translates to:
  /// **'Connection'**
  String get titleConnection;

  /// No description provided for @titleLocalAgent.
  ///
  /// In en, this message translates to:
  /// **'Local Agent'**
  String get titleLocalAgent;

  /// No description provided for @titleOverview.
  ///
  /// In en, this message translates to:
  /// **'Overview'**
  String get titleOverview;

  /// No description provided for @titleAgentLogs.
  ///
  /// In en, this message translates to:
  /// **'Agent Logs'**
  String get titleAgentLogs;

  /// No description provided for @titleRegistration.
  ///
  /// In en, this message translates to:
  /// **'Registration'**
  String get titleRegistration;

  /// No description provided for @titleLocalAgentProcess.
  ///
  /// In en, this message translates to:
  /// **'Local Agent Process'**
  String get titleLocalAgentProcess;

  /// No description provided for @titleUnregisterAgent.
  ///
  /// In en, this message translates to:
  /// **'Unregister Agent'**
  String get titleUnregisterAgent;

  /// No description provided for @titleClearLogs.
  ///
  /// In en, this message translates to:
  /// **'Clear Logs'**
  String get titleClearLogs;

  /// No description provided for @titleClearAllData.
  ///
  /// In en, this message translates to:
  /// **'Clear All Data'**
  String get titleClearAllData;

  /// No description provided for @titleNotConnected.
  ///
  /// In en, this message translates to:
  /// **'Not Connected'**
  String get titleNotConnected;

  /// No description provided for @titleError.
  ///
  /// In en, this message translates to:
  /// **'Error'**
  String get titleError;

  /// No description provided for @titleNoRules.
  ///
  /// In en, this message translates to:
  /// **'No Rules'**
  String get titleNoRules;

  /// No description provided for @titleLocalStorage.
  ///
  /// In en, this message translates to:
  /// **'Local Storage'**
  String get titleLocalStorage;

  /// No description provided for @titleSystem.
  ///
  /// In en, this message translates to:
  /// **'System'**
  String get titleSystem;

  /// No description provided for @titleAbout.
  ///
  /// In en, this message translates to:
  /// **'About'**
  String get titleAbout;

  /// No description provided for @titleExportProfile.
  ///
  /// In en, this message translates to:
  /// **'Export Profile'**
  String get titleExportProfile;

  /// No description provided for @titleStartAtLogin.
  ///
  /// In en, this message translates to:
  /// **'Start at Login'**
  String get titleStartAtLogin;

  /// No description provided for @titleAppearance.
  ///
  /// In en, this message translates to:
  /// **'Appearance'**
  String get titleAppearance;

  /// No description provided for @titleThemeMode.
  ///
  /// In en, this message translates to:
  /// **'Theme Mode'**
  String get titleThemeMode;

  /// No description provided for @titleAccentColor.
  ///
  /// In en, this message translates to:
  /// **'Accent Color'**
  String get titleAccentColor;

  /// No description provided for @titleConnectToMaster.
  ///
  /// In en, this message translates to:
  /// **'Connect to Master'**
  String get titleConnectToMaster;

  /// No description provided for @titleNewRule.
  ///
  /// In en, this message translates to:
  /// **'New Rule'**
  String get titleNewRule;

  /// No description provided for @titleEditRule.
  ///
  /// In en, this message translates to:
  /// **'Edit Rule'**
  String get titleEditRule;

  /// No description provided for @titleDeleteRule.
  ///
  /// In en, this message translates to:
  /// **'Delete Rule'**
  String get titleDeleteRule;

  /// No description provided for @titleDeleteRelay.
  ///
  /// In en, this message translates to:
  /// **'Delete Relay Listener'**
  String get titleDeleteRelay;

  /// No description provided for @titleNoCertificates.
  ///
  /// In en, this message translates to:
  /// **'No Certificates'**
  String get titleNoCertificates;

  /// No description provided for @titleNoRemoteAgents.
  ///
  /// In en, this message translates to:
  /// **'No remote agents'**
  String get titleNoRemoteAgents;

  /// No description provided for @titleNoRelayListeners.
  ///
  /// In en, this message translates to:
  /// **'No Relay Listeners'**
  String get titleNoRelayListeners;

  /// No description provided for @titleQuickActions.
  ///
  /// In en, this message translates to:
  /// **'Quick Actions'**
  String get titleQuickActions;

  /// No description provided for @titleRemoteAgents.
  ///
  /// In en, this message translates to:
  /// **'Remote Agents'**
  String get titleRemoteAgents;

  /// No description provided for @titleSelfSigned.
  ///
  /// In en, this message translates to:
  /// **'Self-signed'**
  String get titleSelfSigned;

  /// No description provided for @descRegisterAgent.
  ///
  /// In en, this message translates to:
  /// **'Connect this client to a master server. You will need a register token from the server.'**
  String get descRegisterAgent;

  /// No description provided for @descUnregisterConfirm.
  ///
  /// In en, this message translates to:
  /// **'This will remove the local registration. The agent on the master server will need to be re-registered.'**
  String get descUnregisterConfirm;

  /// No description provided for @descClearLogs.
  ///
  /// In en, this message translates to:
  /// **'This only clears the displayed logs. The log file on disk is not affected.'**
  String get descClearLogs;

  /// No description provided for @descClearAllData.
  ///
  /// In en, this message translates to:
  /// **'This will erase all local data including your registration profile. The agent on the master server will remain but this client will need to be re-registered.'**
  String get descClearAllData;

  /// No description provided for @descNotConnected.
  ///
  /// In en, this message translates to:
  /// **'Register your agent on the Agent page to view rules from the master server.'**
  String get descNotConnected;

  /// No description provided for @descNoRules.
  ///
  /// In en, this message translates to:
  /// **'No proxy rules are configured on the master server.'**
  String get descNoRules;

  /// No description provided for @descRegisterClient.
  ///
  /// In en, this message translates to:
  /// **'Register this client to connect to a master server.'**
  String get descRegisterClient;

  /// No description provided for @descUnableDetermineStatus.
  ///
  /// In en, this message translates to:
  /// **'Unable to determine agent status'**
  String get descUnableDetermineStatus;

  /// No description provided for @descExportProfile.
  ///
  /// In en, this message translates to:
  /// **'Copy profile JSON to clipboard'**
  String get descExportProfile;

  /// No description provided for @descClearData.
  ///
  /// In en, this message translates to:
  /// **'Remove registration and local cache'**
  String get descClearData;

  /// No description provided for @descStartAtLogin.
  ///
  /// In en, this message translates to:
  /// **'Launch client when system starts'**
  String get descStartAtLogin;

  /// No description provided for @descPleaseConnectFirst.
  ///
  /// In en, this message translates to:
  /// **'Please connect to a Master server first'**
  String get descPleaseConnectFirst;

  /// No description provided for @descCreateFirstRule.
  ///
  /// In en, this message translates to:
  /// **'Create your first proxy rule to get started'**
  String get descCreateFirstRule;

  /// No description provided for @descImportOrRequestCert.
  ///
  /// In en, this message translates to:
  /// **'Import or request SSL certificates to get started'**
  String get descImportOrRequestCert;

  /// No description provided for @descRemoteAgentsAppearHere.
  ///
  /// In en, this message translates to:
  /// **'Remote agents that register with this master will appear here.'**
  String get descRemoteAgentsAppearHere;

  /// No description provided for @descRelayListenersAppearHere.
  ///
  /// In en, this message translates to:
  /// **'Relay listeners will appear here once configured'**
  String get descRelayListenersAppearHere;

  /// No description provided for @descDeleteRuleConfirm.
  ///
  /// In en, this message translates to:
  /// **'Are you sure you want to delete \"{domain}\"? This action cannot be undone.'**
  String descDeleteRuleConfirm(String domain);

  /// No description provided for @descDeleteRelayConfirm.
  ///
  /// In en, this message translates to:
  /// **'Are you sure you want to delete \"{address}\" ({protocol})? This action cannot be undone.'**
  String descDeleteRelayConfirm(String address, String protocol);

  /// No description provided for @descSystemRunningNormal.
  ///
  /// In en, this message translates to:
  /// **'System running normally'**
  String get descSystemRunningNormal;

  /// No description provided for @descAllAgentsOnlineLastSync.
  ///
  /// In en, this message translates to:
  /// **'All agents online · Last sync: 30s ago'**
  String get descAllAgentsOnlineLastSync;

  /// No description provided for @descNotRunning.
  ///
  /// In en, this message translates to:
  /// **'Not Running'**
  String get descNotRunning;

  /// No description provided for @descNotAvailable.
  ///
  /// In en, this message translates to:
  /// **'Not Available'**
  String get descNotAvailable;

  /// No description provided for @descNoAgentAssigned.
  ///
  /// In en, this message translates to:
  /// **'No agent assigned'**
  String get descNoAgentAssigned;

  /// No description provided for @descNotConnectedMaster.
  ///
  /// In en, this message translates to:
  /// **'Not connected'**
  String get descNotConnectedMaster;

  /// No description provided for @msgRegistered.
  ///
  /// In en, this message translates to:
  /// **'Registered agent {agentId}'**
  String msgRegistered(String agentId);

  /// No description provided for @msgUnregistered.
  ///
  /// In en, this message translates to:
  /// **'Unregistered'**
  String get msgUnregistered;

  /// No description provided for @msgAgentStarted.
  ///
  /// In en, this message translates to:
  /// **'Agent started (PID: {pid})'**
  String msgAgentStarted(String pid);

  /// No description provided for @msgAgentStopped.
  ///
  /// In en, this message translates to:
  /// **'Agent stopped'**
  String get msgAgentStopped;

  /// No description provided for @msgAgentAction.
  ///
  /// In en, this message translates to:
  /// **'Agent {action}'**
  String msgAgentAction(String action);

  /// No description provided for @msgActionFailed.
  ///
  /// In en, this message translates to:
  /// **'Failed: {error}'**
  String msgActionFailed(String error);

  /// No description provided for @msgCopied.
  ///
  /// In en, this message translates to:
  /// **'Copied'**
  String get msgCopied;

  /// No description provided for @msgCopiedToClipboard.
  ///
  /// In en, this message translates to:
  /// **'Copied to clipboard'**
  String get msgCopiedToClipboard;

  /// No description provided for @msgProfileExported.
  ///
  /// In en, this message translates to:
  /// **'Profile JSON copied to clipboard'**
  String get msgProfileExported;

  /// No description provided for @msgNoProfileToExport.
  ///
  /// In en, this message translates to:
  /// **'No registered profile to export'**
  String get msgNoProfileToExport;

  /// No description provided for @msgAllDataCleared.
  ///
  /// In en, this message translates to:
  /// **'All local data cleared'**
  String get msgAllDataCleared;

  /// No description provided for @msgStartAtLoginEnabled.
  ///
  /// In en, this message translates to:
  /// **'Start at login enabled (placeholder)'**
  String get msgStartAtLoginEnabled;

  /// No description provided for @msgStartAtLoginDisabled.
  ///
  /// In en, this message translates to:
  /// **'Start at login disabled (placeholder)'**
  String get msgStartAtLoginDisabled;

  /// No description provided for @msgLastUpdated.
  ///
  /// In en, this message translates to:
  /// **'Last updated: {time}'**
  String msgLastUpdated(String time);

  /// No description provided for @msgNoLogs.
  ///
  /// In en, this message translates to:
  /// **'No logs available.\nStart the agent to see logs.'**
  String get msgNoLogs;

  /// No description provided for @msgLogsCleared.
  ///
  /// In en, this message translates to:
  /// **'Logs view cleared'**
  String get msgLogsCleared;

  /// No description provided for @msgRuleCopiedToClipboard.
  ///
  /// In en, this message translates to:
  /// **'Rule copied to clipboard'**
  String get msgRuleCopiedToClipboard;

  /// No description provided for @msgFailedToSaveRule.
  ///
  /// In en, this message translates to:
  /// **'Failed to save rule: {error}'**
  String msgFailedToSaveRule(String error);

  /// No description provided for @labelApplication.
  ///
  /// In en, this message translates to:
  /// **'Application'**
  String get labelApplication;

  /// No description provided for @labelVersion.
  ///
  /// In en, this message translates to:
  /// **'Version'**
  String get labelVersion;

  /// No description provided for @labelDistribution.
  ///
  /// In en, this message translates to:
  /// **'Distribution'**
  String get labelDistribution;

  /// No description provided for @labelContainerPolicy.
  ///
  /// In en, this message translates to:
  /// **'Container Policy'**
  String get labelContainerPolicy;

  /// No description provided for @valueAppName.
  ///
  /// In en, this message translates to:
  /// **'NRE Client'**
  String get valueAppName;

  /// No description provided for @valueGithubRelease.
  ///
  /// In en, this message translates to:
  /// **'GitHub Release'**
  String get valueGithubRelease;

  /// No description provided for @valueContainerPolicyDesc.
  ///
  /// In en, this message translates to:
  /// **'Client artifacts are not embedded in the control-plane image'**
  String get valueContainerPolicyDesc;

  /// No description provided for @valueLoading.
  ///
  /// In en, this message translates to:
  /// **'Loading...'**
  String get valueLoading;

  /// No description provided for @valueDash.
  ///
  /// In en, this message translates to:
  /// **'-'**
  String get valueDash;

  /// No description provided for @valueAppVersion.
  ///
  /// In en, this message translates to:
  /// **'v2.1.0'**
  String get valueAppVersion;

  /// No description provided for @titleAgentProcessControl.
  ///
  /// In en, this message translates to:
  /// **'Agent Process Control'**
  String get titleAgentProcessControl;

  /// No description provided for @labelTheme.
  ///
  /// In en, this message translates to:
  /// **'Theme'**
  String get labelTheme;

  /// No description provided for @valueThemeSystem.
  ///
  /// In en, this message translates to:
  /// **'System'**
  String get valueThemeSystem;

  /// No description provided for @valueThemeLight.
  ///
  /// In en, this message translates to:
  /// **'Light'**
  String get valueThemeLight;

  /// No description provided for @valueThemeDark.
  ///
  /// In en, this message translates to:
  /// **'Dark'**
  String get valueThemeDark;

  /// No description provided for @trayShow.
  ///
  /// In en, this message translates to:
  /// **'Show'**
  String get trayShow;

  /// No description provided for @trayQuit.
  ///
  /// In en, this message translates to:
  /// **'Quit'**
  String get trayQuit;

  /// No description provided for @filterStatus.
  ///
  /// In en, this message translates to:
  /// **'Status'**
  String get filterStatus;

  /// No description provided for @filterType.
  ///
  /// In en, this message translates to:
  /// **'Type'**
  String get filterType;

  /// No description provided for @filterAllStatus.
  ///
  /// In en, this message translates to:
  /// **'All Status'**
  String get filterAllStatus;

  /// No description provided for @filterAllProtocols.
  ///
  /// In en, this message translates to:
  /// **'All Protocols'**
  String get filterAllProtocols;

  /// No description provided for @certStatusValid.
  ///
  /// In en, this message translates to:
  /// **'Valid'**
  String get certStatusValid;

  /// No description provided for @certStatusExpiring.
  ///
  /// In en, this message translates to:
  /// **'Expiring'**
  String get certStatusExpiring;

  /// No description provided for @certStatusExpired.
  ///
  /// In en, this message translates to:
  /// **'Expired'**
  String get certStatusExpired;

  /// No description provided for @labelOverdue.
  ///
  /// In en, this message translates to:
  /// **'overdue'**
  String get labelOverdue;

  /// No description provided for @labelRemaining.
  ///
  /// In en, this message translates to:
  /// **'remaining'**
  String get labelRemaining;

  /// No description provided for @labelIssued.
  ///
  /// In en, this message translates to:
  /// **'Issued: {date}'**
  String labelIssued(String date);

  /// No description provided for @labelUsedBy.
  ///
  /// In en, this message translates to:
  /// **'Used by:'**
  String get labelUsedBy;

  /// No description provided for @labelAgent.
  ///
  /// In en, this message translates to:
  /// **'Agent: {name}'**
  String labelAgent(String name);

  /// No description provided for @labelCertificateCount.
  ///
  /// In en, this message translates to:
  /// **'{count} certificate{plural}'**
  String labelCertificateCount(int count, String plural);

  /// No description provided for @labelRelayCount.
  ///
  /// In en, this message translates to:
  /// **'{count} relay{plural}'**
  String labelRelayCount(int count, String plural);

  /// No description provided for @labelRegisteredCount.
  ///
  /// In en, this message translates to:
  /// **'{count} registered'**
  String labelRegisteredCount(int count);

  /// No description provided for @labelDisabledCount.
  ///
  /// In en, this message translates to:
  /// **'{count} disabled'**
  String labelDisabledCount(int count);

  /// No description provided for @labelAllOnline.
  ///
  /// In en, this message translates to:
  /// **'All online'**
  String get labelAllOnline;

  /// No description provided for @labelOffline.
  ///
  /// In en, this message translates to:
  /// **'{count} offline'**
  String labelOffline(int count);

  /// No description provided for @labelExpiringWarning.
  ///
  /// In en, this message translates to:
  /// **'{count} certificate{plural} expiring within 14 days'**
  String labelExpiringWarning(int count, String plural);

  /// No description provided for @labelReview.
  ///
  /// In en, this message translates to:
  /// **'Review →'**
  String get labelReview;

  /// No description provided for @stepServerUrl.
  ///
  /// In en, this message translates to:
  /// **'Server URL'**
  String get stepServerUrl;

  /// No description provided for @stepRegisterToken.
  ///
  /// In en, this message translates to:
  /// **'Register Token'**
  String get stepRegisterToken;

  /// No description provided for @stepClientName.
  ///
  /// In en, this message translates to:
  /// **'Client Name'**
  String get stepClientName;

  /// No description provided for @actionNewRule.
  ///
  /// In en, this message translates to:
  /// **'New Rule'**
  String get actionNewRule;

  /// No description provided for @actionAddCertificate.
  ///
  /// In en, this message translates to:
  /// **'Add Certificate'**
  String get actionAddCertificate;

  /// No description provided for @actionAddAgent.
  ///
  /// In en, this message translates to:
  /// **'Add Agent'**
  String get actionAddAgent;

  /// No description provided for @actionNewRelay.
  ///
  /// In en, this message translates to:
  /// **'New Relay'**
  String get actionNewRelay;

  /// No description provided for @metaUptime.
  ///
  /// In en, this message translates to:
  /// **'Uptime'**
  String get metaUptime;

  /// No description provided for @metaVersion.
  ///
  /// In en, this message translates to:
  /// **'Version'**
  String get metaVersion;

  /// No description provided for @metaLastSync.
  ///
  /// In en, this message translates to:
  /// **'Last sync'**
  String get metaLastSync;

  /// No description provided for @metaSync30sAgo.
  ///
  /// In en, this message translates to:
  /// **'30s ago'**
  String get metaSync30sAgo;

  /// No description provided for @failedToLoadDashboard.
  ///
  /// In en, this message translates to:
  /// **'Failed to load dashboard'**
  String get failedToLoadDashboard;

  /// No description provided for @failedToLoadRules.
  ///
  /// In en, this message translates to:
  /// **'Failed to load rules'**
  String get failedToLoadRules;

  /// No description provided for @failedToLoadCertificates.
  ///
  /// In en, this message translates to:
  /// **'Failed to load certificates'**
  String get failedToLoadCertificates;

  /// No description provided for @failedToLoadRelays.
  ///
  /// In en, this message translates to:
  /// **'Failed to load relay listeners'**
  String get failedToLoadRelays;

  /// No description provided for @navMore.
  ///
  /// In en, this message translates to:
  /// **'More'**
  String get navMore;
}

class _AppLocalizationsDelegate
    extends LocalizationsDelegate<AppLocalizations> {
  const _AppLocalizationsDelegate();

  @override
  Future<AppLocalizations> load(Locale locale) {
    return SynchronousFuture<AppLocalizations>(lookupAppLocalizations(locale));
  }

  @override
  bool isSupported(Locale locale) =>
      <String>['en', 'zh'].contains(locale.languageCode);

  @override
  bool shouldReload(_AppLocalizationsDelegate old) => false;
}

AppLocalizations lookupAppLocalizations(Locale locale) {
  // Lookup logic when only language code is specified.
  switch (locale.languageCode) {
    case 'en':
      return AppLocalizationsEn();
    case 'zh':
      return AppLocalizationsZh();
  }

  throw FlutterError(
    'AppLocalizations.delegate failed to load unsupported locale "$locale". This is likely '
    'an issue with the localizations generation tool. Please file an issue '
    'on GitHub with a reproducible sample app and the gen-l10n configuration '
    'that was used.',
  );
}
