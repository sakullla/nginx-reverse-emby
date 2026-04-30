// GENERATED CODE - DO NOT MODIFY BY HAND

part of 'rule_models.dart';

// **************************************************************************
// JsonSerializableGenerator
// **************************************************************************

_ProxyRule _$ProxyRuleFromJson(Map<String, dynamic> json) => _ProxyRule(
  id: json['id'] as String,
  domain: json['domain'] as String,
  target: json['target'] as String,
  type: json['type'] as String,
  enabled: json['enabled'] as bool? ?? true,
);

Map<String, dynamic> _$ProxyRuleToJson(_ProxyRule instance) =>
    <String, dynamic>{
      'id': instance.id,
      'domain': instance.domain,
      'target': instance.target,
      'type': instance.type,
      'enabled': instance.enabled,
    };

_CreateRuleRequest _$CreateRuleRequestFromJson(Map<String, dynamic> json) =>
    _CreateRuleRequest(
      domain: json['domain'] as String,
      target: json['target'] as String,
      type: json['type'] as String,
    );

Map<String, dynamic> _$CreateRuleRequestToJson(_CreateRuleRequest instance) =>
    <String, dynamic>{
      'domain': instance.domain,
      'target': instance.target,
      'type': instance.type,
    };

_UpdateRuleRequest _$UpdateRuleRequestFromJson(Map<String, dynamic> json) =>
    _UpdateRuleRequest(
      domain: json['domain'] as String?,
      target: json['target'] as String?,
      type: json['type'] as String?,
      enabled: json['enabled'] as bool?,
    );

Map<String, dynamic> _$UpdateRuleRequestToJson(_UpdateRuleRequest instance) =>
    <String, dynamic>{
      'domain': instance.domain,
      'target': instance.target,
      'type': instance.type,
      'enabled': instance.enabled,
    };
