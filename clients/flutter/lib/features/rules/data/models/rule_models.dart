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
  const UpdateRuleRequest({
    this.domain,
    this.target,
    this.type,
    this.enabled,
  });

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
