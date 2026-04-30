// ignore: unused_import
import 'package:intl/intl.dart' as intl;
import 'app_localizations.dart';

// ignore_for_file: type=lint

/// The translations for Chinese (`zh`).
class AppLocalizationsZh extends AppLocalizations {
  AppLocalizationsZh([String locale = 'zh']) : super(locale);

  @override
  String get appTitle => 'NRE 客户端';

  @override
  String get navDashboard => '仪表盘';

  @override
  String get navAgent => '代理';

  @override
  String get navRules => '规则';

  @override
  String get navSettings => '设置';

  @override
  String get statusRegistered => '已注册';

  @override
  String get statusNotRegistered => '未注册';

  @override
  String get statusRunning => '运行中';

  @override
  String get statusStopped => '已停止';

  @override
  String get statusUnavailable => '不可用';

  @override
  String get statusUnknown => '未知';

  @override
  String get statusChecking => '检测中';

  @override
  String get labelMasterUrl => '主控地址';

  @override
  String get labelAgentId => '代理 ID';

  @override
  String get labelDisplayName => '显示名称';

  @override
  String get labelToken => '令牌';

  @override
  String get labelClientName => '客户端名称';

  @override
  String get labelRegisterToken => '注册令牌';

  @override
  String get labelPid => '进程 ID';

  @override
  String get labelBinaryPath => '可执行文件路径';

  @override
  String get labelDataDir => '数据目录';

  @override
  String get labelLogPath => '日志路径';

  @override
  String get labelMessage => '消息';

  @override
  String get labelPlatform => '平台';

  @override
  String get labelAgentStatus => '代理状态';

  @override
  String get labelType => '类型';

  @override
  String get labelTarget => '目标';

  @override
  String get labelEnabled => '已启用';

  @override
  String get labelDisabled => '已禁用';

  @override
  String get labelNotConfigured => '未配置';

  @override
  String get labelNotRegistered => '未注册';

  @override
  String get hintMasterUrl => 'https://your-server.com';

  @override
  String get hintRegisterToken => '输入主控服务器提供的令牌';

  @override
  String get hintClientName => 'nre-client';

  @override
  String get errorRequiredMasterUrl => '主控地址不能为空';

  @override
  String get errorRequiredRegisterToken => '注册令牌不能为空';

  @override
  String errorRegistrationFailed(String error) {
    return '注册失败: $error';
  }

  @override
  String get errorMasterUrlScheme => '主控地址必须使用 http 或 https 协议';

  @override
  String get errorMasterUrlHost => '主控地址必须包含主机名';

  @override
  String get errorNoAgentId => '注册响应中未包含代理 ID';

  @override
  String errorInvalidResponse(String message) {
    return '后端响应无效: $message';
  }

  @override
  String get btnRegister => '注册';

  @override
  String get btnUnregister => '注销';

  @override
  String get btnCancel => '取消';

  @override
  String get btnClear => '清空';

  @override
  String get btnCopy => '复制';

  @override
  String get btnCopyId => '复制 ID';

  @override
  String get btnStart => '启动';

  @override
  String get btnStop => '停止';

  @override
  String get btnRestart => '重启';

  @override
  String get btnRefresh => '刷新';

  @override
  String get btnRetry => '重试';

  @override
  String get btnViewDetails => '查看详情';

  @override
  String get btnRegisterNow => '立即注册';

  @override
  String get titleRegisterAgent => '注册代理';

  @override
  String get titleAgent => '代理';

  @override
  String get titleDashboard => '仪表盘';

  @override
  String get titleRules => '规则';

  @override
  String get titleSettings => '设置';

  @override
  String get titleControl => '控制';

  @override
  String get titleLogs => '日志';

  @override
  String get titleConnection => '连接';

  @override
  String get titleLocalAgent => '本地代理';

  @override
  String get titleOverview => '概览';

  @override
  String get titleAgentLogs => '代理日志';

  @override
  String get titleRegistration => '注册信息';

  @override
  String get titleLocalAgentProcess => '本地代理进程';

  @override
  String get titleUnregisterAgent => '注销代理';

  @override
  String get titleClearLogs => '清空日志';

  @override
  String get titleClearAllData => '清空所有数据';

  @override
  String get titleNotConnected => '未连接';

  @override
  String get titleError => '错误';

  @override
  String get titleNoRules => '无规则';

  @override
  String get titleLocalStorage => '本地存储';

  @override
  String get titleSystem => '系统';

  @override
  String get titleAbout => '关于';

  @override
  String get titleExportProfile => '导出配置';

  @override
  String get titleStartAtLogin => '开机自启';

  @override
  String get descRegisterAgent => '将本客户端连接到主控服务器。你需要从服务器获取注册令牌。';

  @override
  String get descUnregisterConfirm => '这将移除本地注册信息。主控服务器上的代理将需要重新注册。';

  @override
  String get descClearLogs => '此操作仅清空显示的日志。磁盘上的日志文件不受影响。';

  @override
  String get descClearAllData =>
      '这将清除所有本地数据，包括注册信息。主控服务器上的代理将继续存在，但本客户端需要重新注册。';

  @override
  String get descNotConnected => '请在代理页面注册，以查看主控服务器的规则。';

  @override
  String get descNoRules => '主控服务器上未配置代理规则。';

  @override
  String get descRegisterClient => '注册本客户端以连接到主控服务器。';

  @override
  String get descUnableDetermineStatus => '无法确定代理状态';

  @override
  String get descExportProfile => '将配置 JSON 复制到剪贴板';

  @override
  String get descClearData => '移除注册信息和本地缓存';

  @override
  String get descStartAtLogin => '系统启动时自动运行客户端';

  @override
  String msgRegistered(String agentId) {
    return '已注册代理 $agentId';
  }

  @override
  String get msgUnregistered => '已注销';

  @override
  String msgAgentStarted(String pid) {
    return '代理已启动 (PID: $pid)';
  }

  @override
  String get msgAgentStopped => '代理已停止';

  @override
  String msgAgentAction(String action) {
    return '代理已$action';
  }

  @override
  String msgActionFailed(String error) {
    return '操作失败: $error';
  }

  @override
  String get msgCopied => '已复制';

  @override
  String get msgCopiedToClipboard => '已复制到剪贴板';

  @override
  String get msgProfileExported => '配置 JSON 已复制到剪贴板';

  @override
  String get msgNoProfileToExport => '没有已注册的配置可供导出';

  @override
  String get msgAllDataCleared => '所有本地数据已清除';

  @override
  String get msgStartAtLoginEnabled => '开机自启已启用（占位）';

  @override
  String get msgStartAtLoginDisabled => '开机自启已禁用（占位）';

  @override
  String msgLastUpdated(String time) {
    return '最后更新: $time';
  }

  @override
  String get msgNoLogs => '暂无日志。\n启动代理以查看日志。';

  @override
  String get msgLogsCleared => '日志视图已清空';

  @override
  String get labelApplication => '应用';

  @override
  String get labelVersion => '版本';

  @override
  String get labelDistribution => '发行方式';

  @override
  String get labelContainerPolicy => '容器策略';

  @override
  String get valueAppName => 'NRE 客户端';

  @override
  String get valueGithubRelease => 'GitHub 发布';

  @override
  String get valueContainerPolicyDesc => '客户端构件未嵌入控制平面镜像中';

  @override
  String get valueLoading => '加载中...';

  @override
  String get valueDash => '-';

  @override
  String get titleAgentProcessControl => '代理进程控制';
}
