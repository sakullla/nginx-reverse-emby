String stringId(dynamic value) => value?.toString() ?? '';

List<String> stringList(dynamic value) =>
    value is List ? value.map((item) => item.toString()).toList() : const [];

class HttpBackend {
  const HttpBackend({required this.url, this.weight, this.name});

  final String url;
  final int? weight;
  final String? name;

  HttpBackend copyWith({String? url, int? weight, String? name}) => HttpBackend(
    url: url ?? this.url,
    weight: weight ?? this.weight,
    name: name ?? this.name,
  );

  factory HttpBackend.fromJson(Map<String, dynamic> json) => HttpBackend(
    url: json['url']?.toString() ?? '',
    weight: json['weight'] is int ? json['weight'] as int : null,
    name: json['name']?.toString(),
  );

  Map<String, dynamic> toJson() => {
    'url': url,
    if (weight != null) 'weight': weight,
    if (name != null) 'name': name,
  };
}

class HttpHeaderEntry {
  const HttpHeaderEntry({required this.name, required this.value});

  final String name;
  final String value;

  HttpHeaderEntry copyWith({String? name, String? value}) =>
      HttpHeaderEntry(name: name ?? this.name, value: value ?? this.value);

  factory HttpHeaderEntry.fromJson(Map<String, dynamic> json) =>
      HttpHeaderEntry(
        name: json['name']?.toString() ?? '',
        value: json['value']?.toString() ?? '',
      );

  Map<String, dynamic> toJson() => {'name': name, 'value': value};
}

class HttpProxyRule {
  const HttpProxyRule({
    required this.id,
    required this.frontendUrl,
    required this.backends,
    this.enabled = true,
    this.tags = const [],
    this.proxyRedirect,
    this.passProxyHeaders,
    this.userAgent,
    this.customHeaders = const [],
    this.loadBalancingStrategy,
    this.relayLayers = const [],
    this.relayObfs,
  });

  final String id;
  final String frontendUrl;
  final List<HttpBackend> backends;
  final bool enabled;
  final List<String> tags;
  final bool? proxyRedirect;
  final bool? passProxyHeaders;
  final String? userAgent;
  final List<HttpHeaderEntry> customHeaders;
  final String? loadBalancingStrategy;
  final List<List<int>> relayLayers;
  final bool? relayObfs;

  String get backendUrl => backends.isNotEmpty ? backends.first.url : '';

  factory HttpProxyRule.fromJson(Map<String, dynamic> json) {
    final parsedBackends = json['backends'] is List
        ? (json['backends'] as List)
              .whereType<Map<String, dynamic>>()
              .map(HttpBackend.fromJson)
              .toList()
        : <HttpBackend>[];
    final backendUrl = json['backend_url']?.toString();
    final backends = parsedBackends.isNotEmpty
        ? parsedBackends
        : [
            if (backendUrl != null && backendUrl.isNotEmpty)
              HttpBackend(url: backendUrl),
          ];
    final loadBalancing = json['load_balancing'];

    return HttpProxyRule(
      id: stringId(json['id']),
      frontendUrl: json['frontend_url']?.toString() ?? '',
      backends: backends,
      enabled: json['enabled'] as bool? ?? true,
      tags: stringList(json['tags']),
      proxyRedirect: json['proxy_redirect'] as bool?,
      passProxyHeaders: json['pass_proxy_headers'] as bool?,
      userAgent: json['user_agent']?.toString(),
      customHeaders: json['custom_headers'] is List
          ? (json['custom_headers'] as List)
                .whereType<Map<String, dynamic>>()
                .map(HttpHeaderEntry.fromJson)
                .toList()
          : const [],
      loadBalancingStrategy: loadBalancing is Map
          ? loadBalancing['strategy']?.toString()
          : json['load_balancing_strategy']?.toString(),
      relayLayers: _relayLayersFromJson(json['relay_layers']),
      relayObfs: json['relay_obfs'] as bool?,
    );
  }

  Map<String, dynamic> toJson() => {
    'id': id,
    'frontend_url': frontendUrl,
    'backend_url': backendUrl,
    'backends': backends.map((backend) => backend.toJson()).toList(),
    'enabled': enabled,
    'tags': tags,
    if (proxyRedirect != null) 'proxy_redirect': proxyRedirect,
    if (passProxyHeaders != null) 'pass_proxy_headers': passProxyHeaders,
    if (userAgent != null) 'user_agent': userAgent,
    'custom_headers': customHeaders.map((header) => header.toJson()).toList(),
    if (loadBalancingStrategy != null)
      'load_balancing': {'strategy': loadBalancingStrategy},
    'relay_layers': relayLayers,
    if (relayObfs != null) 'relay_obfs': relayObfs,
  };

  HttpProxyRule copyWith({
    String? id,
    String? frontendUrl,
    List<HttpBackend>? backends,
    bool? enabled,
    List<String>? tags,
    bool? proxyRedirect,
    bool? passProxyHeaders,
    String? userAgent,
    List<HttpHeaderEntry>? customHeaders,
    String? loadBalancingStrategy,
    List<List<int>>? relayLayers,
    bool? relayObfs,
  }) => HttpProxyRule(
    id: id ?? this.id,
    frontendUrl: frontendUrl ?? this.frontendUrl,
    backends: backends ?? this.backends,
    enabled: enabled ?? this.enabled,
    tags: tags ?? this.tags,
    proxyRedirect: proxyRedirect ?? this.proxyRedirect,
    passProxyHeaders: passProxyHeaders ?? this.passProxyHeaders,
    userAgent: userAgent ?? this.userAgent,
    customHeaders: customHeaders ?? this.customHeaders,
    loadBalancingStrategy: loadBalancingStrategy ?? this.loadBalancingStrategy,
    relayLayers: relayLayers ?? this.relayLayers,
    relayObfs: relayObfs ?? this.relayObfs,
  );
}

class CreateHttpRuleRequest {
  const CreateHttpRuleRequest({
    required this.frontendUrl,
    required this.backends,
    this.enabled = true,
    this.tags = const [],
    this.proxyRedirect,
    this.passProxyHeaders,
    this.userAgent,
    this.customHeaders = const [],
    this.loadBalancingStrategy,
    this.relayLayers = const [],
    this.relayObfs,
  });

  final String frontendUrl;
  final List<HttpBackend> backends;
  final bool enabled;
  final List<String> tags;
  final bool? proxyRedirect;
  final bool? passProxyHeaders;
  final String? userAgent;
  final List<HttpHeaderEntry> customHeaders;
  final String? loadBalancingStrategy;
  final List<List<int>> relayLayers;
  final bool? relayObfs;

  String get backendUrl => backends.isNotEmpty ? backends.first.url : '';

  Map<String, dynamic> toJson() => {
    'frontend_url': frontendUrl,
    'backend_url': backendUrl,
    'backends': backends.map((backend) => backend.toJson()).toList(),
    'enabled': enabled,
    'tags': tags,
    if (proxyRedirect != null) 'proxy_redirect': proxyRedirect,
    if (passProxyHeaders != null) 'pass_proxy_headers': passProxyHeaders,
    if (userAgent != null) 'user_agent': userAgent,
    'custom_headers': customHeaders.map((header) => header.toJson()).toList(),
    if (loadBalancingStrategy != null)
      'load_balancing': {'strategy': loadBalancingStrategy},
    'relay_layers': relayLayers,
    if (relayObfs != null) 'relay_obfs': relayObfs,
  };
}

class UpdateHttpRuleRequest {
  const UpdateHttpRuleRequest({
    this.frontendUrl,
    this.backends,
    this.enabled,
    this.tags,
    this.proxyRedirect,
    this.passProxyHeaders,
    this.userAgent,
    this.customHeaders,
    this.loadBalancingStrategy,
    this.relayLayers,
    this.relayObfs,
  });

  final String? frontendUrl;
  final List<HttpBackend>? backends;
  final bool? enabled;
  final List<String>? tags;
  final bool? proxyRedirect;
  final bool? passProxyHeaders;
  final String? userAgent;
  final List<HttpHeaderEntry>? customHeaders;
  final String? loadBalancingStrategy;
  final List<List<int>>? relayLayers;
  final bool? relayObfs;

  factory UpdateHttpRuleRequest.fromRule(HttpProxyRule rule) =>
      UpdateHttpRuleRequest(
        frontendUrl: rule.frontendUrl,
        backends: rule.backends,
        enabled: rule.enabled,
        tags: rule.tags,
        proxyRedirect: rule.proxyRedirect,
        passProxyHeaders: rule.passProxyHeaders,
        userAgent: rule.userAgent,
        customHeaders: rule.customHeaders,
        loadBalancingStrategy: rule.loadBalancingStrategy,
        relayLayers: rule.relayLayers,
        relayObfs: rule.relayObfs,
      );

  Map<String, dynamic> toJson() => {
    if (frontendUrl != null) 'frontend_url': frontendUrl,
    if (backends != null && backends!.isNotEmpty)
      'backend_url': backends!.first.url,
    if (backends != null)
      'backends': backends!.map((backend) => backend.toJson()).toList(),
    if (enabled != null) 'enabled': enabled,
    if (tags != null) 'tags': tags,
    if (proxyRedirect != null) 'proxy_redirect': proxyRedirect,
    if (passProxyHeaders != null) 'pass_proxy_headers': passProxyHeaders,
    if (userAgent != null) 'user_agent': userAgent,
    if (customHeaders != null)
      'custom_headers': customHeaders!
          .map((header) => header.toJson())
          .toList(),
    if (loadBalancingStrategy != null)
      'load_balancing': {'strategy': loadBalancingStrategy},
    if (relayLayers != null) 'relay_layers': relayLayers,
    if (relayObfs != null) 'relay_obfs': relayObfs,
  };
}

List<List<int>> _relayLayersFromJson(dynamic value) {
  if (value is! List) return const [];
  return value
      .whereType<List>()
      .map(
        (layer) => layer
            .map((item) => item is int ? item : int.tryParse(item.toString()))
            .whereType<int>()
            .toList(),
      )
      .toList();
}

class ProxyRule {
  const ProxyRule({
    required this.id,
    required this.domain,
    required this.target,
    required this.type,
    this.enabled = true,
  });

  final String id;
  final String domain;
  final String target;
  final String type;
  final bool enabled;

  factory ProxyRule.fromJson(Map<String, dynamic> json) => ProxyRule(
    id: json['id'] as String,
    domain: json['domain'] as String,
    target: json['target'] as String,
    type: json['type'] as String,
    enabled: json['enabled'] as bool? ?? true,
  );

  Map<String, dynamic> toJson() => {
    'id': id,
    'domain': domain,
    'target': target,
    'type': type,
    'enabled': enabled,
  };
}

class CreateRuleRequest {
  const CreateRuleRequest({
    required this.domain,
    required this.target,
    required this.type,
  });

  final String domain;
  final String target;
  final String type;

  Map<String, dynamic> toJson() => {
    'domain': domain,
    'target': target,
    'type': type,
  };
}

class UpdateRuleRequest {
  const UpdateRuleRequest({this.domain, this.target, this.type, this.enabled});

  final String? domain;
  final String? target;
  final String? type;
  final bool? enabled;

  Map<String, dynamic> toJson() => {
    if (domain != null) 'domain': domain,
    if (target != null) 'target': target,
    if (type != null) 'type': type,
    if (enabled != null) 'enabled': enabled,
  };
}
