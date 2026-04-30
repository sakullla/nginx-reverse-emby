// GENERATED CODE - DO NOT MODIFY BY HAND
// coverage:ignore-file
// ignore_for_file: type=lint
// ignore_for_file: unused_element, deprecated_member_use, deprecated_member_use_from_same_package, use_function_type_syntax_for_parameters, unnecessary_const, avoid_init_to_null, invalid_override_different_default_values_named, prefer_expression_function_bodies, annotate_overrides, invalid_annotation_target, unnecessary_question_mark

part of 'rule_models.dart';

// **************************************************************************
// FreezedGenerator
// **************************************************************************

// dart format off
T _$identity<T>(T value) => value;

/// @nodoc
mixin _$ProxyRule {

 String get id; String get domain; String get target; String get type; bool get enabled;
/// Create a copy of ProxyRule
/// with the given fields replaced by the non-null parameter values.
@JsonKey(includeFromJson: false, includeToJson: false)
@pragma('vm:prefer-inline')
$ProxyRuleCopyWith<ProxyRule> get copyWith => _$ProxyRuleCopyWithImpl<ProxyRule>(this as ProxyRule, _$identity);

  /// Serializes this ProxyRule to a JSON map.
  Map<String, dynamic> toJson();


@override
bool operator ==(Object other) {
  return identical(this, other) || (other.runtimeType == runtimeType&&other is ProxyRule&&(identical(other.id, id) || other.id == id)&&(identical(other.domain, domain) || other.domain == domain)&&(identical(other.target, target) || other.target == target)&&(identical(other.type, type) || other.type == type)&&(identical(other.enabled, enabled) || other.enabled == enabled));
}

@JsonKey(includeFromJson: false, includeToJson: false)
@override
int get hashCode => Object.hash(runtimeType,id,domain,target,type,enabled);

@override
String toString() {
  return 'ProxyRule(id: $id, domain: $domain, target: $target, type: $type, enabled: $enabled)';
}


}

/// @nodoc
abstract mixin class $ProxyRuleCopyWith<$Res>  {
  factory $ProxyRuleCopyWith(ProxyRule value, $Res Function(ProxyRule) _then) = _$ProxyRuleCopyWithImpl;
@useResult
$Res call({
 String id, String domain, String target, String type, bool enabled
});




}
/// @nodoc
class _$ProxyRuleCopyWithImpl<$Res>
    implements $ProxyRuleCopyWith<$Res> {
  _$ProxyRuleCopyWithImpl(this._self, this._then);

  final ProxyRule _self;
  final $Res Function(ProxyRule) _then;

/// Create a copy of ProxyRule
/// with the given fields replaced by the non-null parameter values.
@pragma('vm:prefer-inline') @override $Res call({Object? id = null,Object? domain = null,Object? target = null,Object? type = null,Object? enabled = null,}) {
  return _then(_self.copyWith(
id: null == id ? _self.id : id // ignore: cast_nullable_to_non_nullable
as String,domain: null == domain ? _self.domain : domain // ignore: cast_nullable_to_non_nullable
as String,target: null == target ? _self.target : target // ignore: cast_nullable_to_non_nullable
as String,type: null == type ? _self.type : type // ignore: cast_nullable_to_non_nullable
as String,enabled: null == enabled ? _self.enabled : enabled // ignore: cast_nullable_to_non_nullable
as bool,
  ));
}

}


/// Adds pattern-matching-related methods to [ProxyRule].
extension ProxyRulePatterns on ProxyRule {
/// A variant of `map` that fallback to returning `orElse`.
///
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case final Subclass value:
///     return ...;
///   case _:
///     return orElse();
/// }
/// ```

@optionalTypeArgs TResult maybeMap<TResult extends Object?>(TResult Function( _ProxyRule value)?  $default,{required TResult orElse(),}){
final _that = this;
switch (_that) {
case _ProxyRule() when $default != null:
return $default(_that);case _:
  return orElse();

}
}
/// A `switch`-like method, using callbacks.
///
/// Callbacks receives the raw object, upcasted.
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case final Subclass value:
///     return ...;
///   case final Subclass2 value:
///     return ...;
/// }
/// ```

@optionalTypeArgs TResult map<TResult extends Object?>(TResult Function( _ProxyRule value)  $default,){
final _that = this;
switch (_that) {
case _ProxyRule():
return $default(_that);case _:
  throw StateError('Unexpected subclass');

}
}
/// A variant of `map` that fallback to returning `null`.
///
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case final Subclass value:
///     return ...;
///   case _:
///     return null;
/// }
/// ```

@optionalTypeArgs TResult? mapOrNull<TResult extends Object?>(TResult? Function( _ProxyRule value)?  $default,){
final _that = this;
switch (_that) {
case _ProxyRule() when $default != null:
return $default(_that);case _:
  return null;

}
}
/// A variant of `when` that fallback to an `orElse` callback.
///
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case Subclass(:final field):
///     return ...;
///   case _:
///     return orElse();
/// }
/// ```

@optionalTypeArgs TResult maybeWhen<TResult extends Object?>(TResult Function( String id,  String domain,  String target,  String type,  bool enabled)?  $default,{required TResult orElse(),}) {final _that = this;
switch (_that) {
case _ProxyRule() when $default != null:
return $default(_that.id,_that.domain,_that.target,_that.type,_that.enabled);case _:
  return orElse();

}
}
/// A `switch`-like method, using callbacks.
///
/// As opposed to `map`, this offers destructuring.
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case Subclass(:final field):
///     return ...;
///   case Subclass2(:final field2):
///     return ...;
/// }
/// ```

@optionalTypeArgs TResult when<TResult extends Object?>(TResult Function( String id,  String domain,  String target,  String type,  bool enabled)  $default,) {final _that = this;
switch (_that) {
case _ProxyRule():
return $default(_that.id,_that.domain,_that.target,_that.type,_that.enabled);case _:
  throw StateError('Unexpected subclass');

}
}
/// A variant of `when` that fallback to returning `null`
///
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case Subclass(:final field):
///     return ...;
///   case _:
///     return null;
/// }
/// ```

@optionalTypeArgs TResult? whenOrNull<TResult extends Object?>(TResult? Function( String id,  String domain,  String target,  String type,  bool enabled)?  $default,) {final _that = this;
switch (_that) {
case _ProxyRule() when $default != null:
return $default(_that.id,_that.domain,_that.target,_that.type,_that.enabled);case _:
  return null;

}
}

}

/// @nodoc
@JsonSerializable()

class _ProxyRule implements ProxyRule {
  const _ProxyRule({required this.id, required this.domain, required this.target, required this.type, this.enabled = true});
  factory _ProxyRule.fromJson(Map<String, dynamic> json) => _$ProxyRuleFromJson(json);

@override final  String id;
@override final  String domain;
@override final  String target;
@override final  String type;
@override@JsonKey() final  bool enabled;

/// Create a copy of ProxyRule
/// with the given fields replaced by the non-null parameter values.
@override @JsonKey(includeFromJson: false, includeToJson: false)
@pragma('vm:prefer-inline')
_$ProxyRuleCopyWith<_ProxyRule> get copyWith => __$ProxyRuleCopyWithImpl<_ProxyRule>(this, _$identity);

@override
Map<String, dynamic> toJson() {
  return _$ProxyRuleToJson(this, );
}

@override
bool operator ==(Object other) {
  return identical(this, other) || (other.runtimeType == runtimeType&&other is _ProxyRule&&(identical(other.id, id) || other.id == id)&&(identical(other.domain, domain) || other.domain == domain)&&(identical(other.target, target) || other.target == target)&&(identical(other.type, type) || other.type == type)&&(identical(other.enabled, enabled) || other.enabled == enabled));
}

@JsonKey(includeFromJson: false, includeToJson: false)
@override
int get hashCode => Object.hash(runtimeType,id,domain,target,type,enabled);

@override
String toString() {
  return 'ProxyRule(id: $id, domain: $domain, target: $target, type: $type, enabled: $enabled)';
}


}

/// @nodoc
abstract mixin class _$ProxyRuleCopyWith<$Res> implements $ProxyRuleCopyWith<$Res> {
  factory _$ProxyRuleCopyWith(_ProxyRule value, $Res Function(_ProxyRule) _then) = __$ProxyRuleCopyWithImpl;
@override @useResult
$Res call({
 String id, String domain, String target, String type, bool enabled
});




}
/// @nodoc
class __$ProxyRuleCopyWithImpl<$Res>
    implements _$ProxyRuleCopyWith<$Res> {
  __$ProxyRuleCopyWithImpl(this._self, this._then);

  final _ProxyRule _self;
  final $Res Function(_ProxyRule) _then;

/// Create a copy of ProxyRule
/// with the given fields replaced by the non-null parameter values.
@override @pragma('vm:prefer-inline') $Res call({Object? id = null,Object? domain = null,Object? target = null,Object? type = null,Object? enabled = null,}) {
  return _then(_ProxyRule(
id: null == id ? _self.id : id // ignore: cast_nullable_to_non_nullable
as String,domain: null == domain ? _self.domain : domain // ignore: cast_nullable_to_non_nullable
as String,target: null == target ? _self.target : target // ignore: cast_nullable_to_non_nullable
as String,type: null == type ? _self.type : type // ignore: cast_nullable_to_non_nullable
as String,enabled: null == enabled ? _self.enabled : enabled // ignore: cast_nullable_to_non_nullable
as bool,
  ));
}


}


/// @nodoc
mixin _$CreateRuleRequest {

 String get domain; String get target; String get type;
/// Create a copy of CreateRuleRequest
/// with the given fields replaced by the non-null parameter values.
@JsonKey(includeFromJson: false, includeToJson: false)
@pragma('vm:prefer-inline')
$CreateRuleRequestCopyWith<CreateRuleRequest> get copyWith => _$CreateRuleRequestCopyWithImpl<CreateRuleRequest>(this as CreateRuleRequest, _$identity);

  /// Serializes this CreateRuleRequest to a JSON map.
  Map<String, dynamic> toJson();


@override
bool operator ==(Object other) {
  return identical(this, other) || (other.runtimeType == runtimeType&&other is CreateRuleRequest&&(identical(other.domain, domain) || other.domain == domain)&&(identical(other.target, target) || other.target == target)&&(identical(other.type, type) || other.type == type));
}

@JsonKey(includeFromJson: false, includeToJson: false)
@override
int get hashCode => Object.hash(runtimeType,domain,target,type);

@override
String toString() {
  return 'CreateRuleRequest(domain: $domain, target: $target, type: $type)';
}


}

/// @nodoc
abstract mixin class $CreateRuleRequestCopyWith<$Res>  {
  factory $CreateRuleRequestCopyWith(CreateRuleRequest value, $Res Function(CreateRuleRequest) _then) = _$CreateRuleRequestCopyWithImpl;
@useResult
$Res call({
 String domain, String target, String type
});




}
/// @nodoc
class _$CreateRuleRequestCopyWithImpl<$Res>
    implements $CreateRuleRequestCopyWith<$Res> {
  _$CreateRuleRequestCopyWithImpl(this._self, this._then);

  final CreateRuleRequest _self;
  final $Res Function(CreateRuleRequest) _then;

/// Create a copy of CreateRuleRequest
/// with the given fields replaced by the non-null parameter values.
@pragma('vm:prefer-inline') @override $Res call({Object? domain = null,Object? target = null,Object? type = null,}) {
  return _then(_self.copyWith(
domain: null == domain ? _self.domain : domain // ignore: cast_nullable_to_non_nullable
as String,target: null == target ? _self.target : target // ignore: cast_nullable_to_non_nullable
as String,type: null == type ? _self.type : type // ignore: cast_nullable_to_non_nullable
as String,
  ));
}

}


/// Adds pattern-matching-related methods to [CreateRuleRequest].
extension CreateRuleRequestPatterns on CreateRuleRequest {
/// A variant of `map` that fallback to returning `orElse`.
///
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case final Subclass value:
///     return ...;
///   case _:
///     return orElse();
/// }
/// ```

@optionalTypeArgs TResult maybeMap<TResult extends Object?>(TResult Function( _CreateRuleRequest value)?  $default,{required TResult orElse(),}){
final _that = this;
switch (_that) {
case _CreateRuleRequest() when $default != null:
return $default(_that);case _:
  return orElse();

}
}
/// A `switch`-like method, using callbacks.
///
/// Callbacks receives the raw object, upcasted.
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case final Subclass value:
///     return ...;
///   case final Subclass2 value:
///     return ...;
/// }
/// ```

@optionalTypeArgs TResult map<TResult extends Object?>(TResult Function( _CreateRuleRequest value)  $default,){
final _that = this;
switch (_that) {
case _CreateRuleRequest():
return $default(_that);case _:
  throw StateError('Unexpected subclass');

}
}
/// A variant of `map` that fallback to returning `null`.
///
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case final Subclass value:
///     return ...;
///   case _:
///     return null;
/// }
/// ```

@optionalTypeArgs TResult? mapOrNull<TResult extends Object?>(TResult? Function( _CreateRuleRequest value)?  $default,){
final _that = this;
switch (_that) {
case _CreateRuleRequest() when $default != null:
return $default(_that);case _:
  return null;

}
}
/// A variant of `when` that fallback to an `orElse` callback.
///
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case Subclass(:final field):
///     return ...;
///   case _:
///     return orElse();
/// }
/// ```

@optionalTypeArgs TResult maybeWhen<TResult extends Object?>(TResult Function( String domain,  String target,  String type)?  $default,{required TResult orElse(),}) {final _that = this;
switch (_that) {
case _CreateRuleRequest() when $default != null:
return $default(_that.domain,_that.target,_that.type);case _:
  return orElse();

}
}
/// A `switch`-like method, using callbacks.
///
/// As opposed to `map`, this offers destructuring.
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case Subclass(:final field):
///     return ...;
///   case Subclass2(:final field2):
///     return ...;
/// }
/// ```

@optionalTypeArgs TResult when<TResult extends Object?>(TResult Function( String domain,  String target,  String type)  $default,) {final _that = this;
switch (_that) {
case _CreateRuleRequest():
return $default(_that.domain,_that.target,_that.type);case _:
  throw StateError('Unexpected subclass');

}
}
/// A variant of `when` that fallback to returning `null`
///
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case Subclass(:final field):
///     return ...;
///   case _:
///     return null;
/// }
/// ```

@optionalTypeArgs TResult? whenOrNull<TResult extends Object?>(TResult? Function( String domain,  String target,  String type)?  $default,) {final _that = this;
switch (_that) {
case _CreateRuleRequest() when $default != null:
return $default(_that.domain,_that.target,_that.type);case _:
  return null;

}
}

}

/// @nodoc
@JsonSerializable()

class _CreateRuleRequest implements CreateRuleRequest {
  const _CreateRuleRequest({required this.domain, required this.target, required this.type});
  factory _CreateRuleRequest.fromJson(Map<String, dynamic> json) => _$CreateRuleRequestFromJson(json);

@override final  String domain;
@override final  String target;
@override final  String type;

/// Create a copy of CreateRuleRequest
/// with the given fields replaced by the non-null parameter values.
@override @JsonKey(includeFromJson: false, includeToJson: false)
@pragma('vm:prefer-inline')
_$CreateRuleRequestCopyWith<_CreateRuleRequest> get copyWith => __$CreateRuleRequestCopyWithImpl<_CreateRuleRequest>(this, _$identity);

@override
Map<String, dynamic> toJson() {
  return _$CreateRuleRequestToJson(this, );
}

@override
bool operator ==(Object other) {
  return identical(this, other) || (other.runtimeType == runtimeType&&other is _CreateRuleRequest&&(identical(other.domain, domain) || other.domain == domain)&&(identical(other.target, target) || other.target == target)&&(identical(other.type, type) || other.type == type));
}

@JsonKey(includeFromJson: false, includeToJson: false)
@override
int get hashCode => Object.hash(runtimeType,domain,target,type);

@override
String toString() {
  return 'CreateRuleRequest(domain: $domain, target: $target, type: $type)';
}


}

/// @nodoc
abstract mixin class _$CreateRuleRequestCopyWith<$Res> implements $CreateRuleRequestCopyWith<$Res> {
  factory _$CreateRuleRequestCopyWith(_CreateRuleRequest value, $Res Function(_CreateRuleRequest) _then) = __$CreateRuleRequestCopyWithImpl;
@override @useResult
$Res call({
 String domain, String target, String type
});




}
/// @nodoc
class __$CreateRuleRequestCopyWithImpl<$Res>
    implements _$CreateRuleRequestCopyWith<$Res> {
  __$CreateRuleRequestCopyWithImpl(this._self, this._then);

  final _CreateRuleRequest _self;
  final $Res Function(_CreateRuleRequest) _then;

/// Create a copy of CreateRuleRequest
/// with the given fields replaced by the non-null parameter values.
@override @pragma('vm:prefer-inline') $Res call({Object? domain = null,Object? target = null,Object? type = null,}) {
  return _then(_CreateRuleRequest(
domain: null == domain ? _self.domain : domain // ignore: cast_nullable_to_non_nullable
as String,target: null == target ? _self.target : target // ignore: cast_nullable_to_non_nullable
as String,type: null == type ? _self.type : type // ignore: cast_nullable_to_non_nullable
as String,
  ));
}


}


/// @nodoc
mixin _$UpdateRuleRequest {

 String? get domain; String? get target; String? get type; bool? get enabled;
/// Create a copy of UpdateRuleRequest
/// with the given fields replaced by the non-null parameter values.
@JsonKey(includeFromJson: false, includeToJson: false)
@pragma('vm:prefer-inline')
$UpdateRuleRequestCopyWith<UpdateRuleRequest> get copyWith => _$UpdateRuleRequestCopyWithImpl<UpdateRuleRequest>(this as UpdateRuleRequest, _$identity);

  /// Serializes this UpdateRuleRequest to a JSON map.
  Map<String, dynamic> toJson();


@override
bool operator ==(Object other) {
  return identical(this, other) || (other.runtimeType == runtimeType&&other is UpdateRuleRequest&&(identical(other.domain, domain) || other.domain == domain)&&(identical(other.target, target) || other.target == target)&&(identical(other.type, type) || other.type == type)&&(identical(other.enabled, enabled) || other.enabled == enabled));
}

@JsonKey(includeFromJson: false, includeToJson: false)
@override
int get hashCode => Object.hash(runtimeType,domain,target,type,enabled);

@override
String toString() {
  return 'UpdateRuleRequest(domain: $domain, target: $target, type: $type, enabled: $enabled)';
}


}

/// @nodoc
abstract mixin class $UpdateRuleRequestCopyWith<$Res>  {
  factory $UpdateRuleRequestCopyWith(UpdateRuleRequest value, $Res Function(UpdateRuleRequest) _then) = _$UpdateRuleRequestCopyWithImpl;
@useResult
$Res call({
 String? domain, String? target, String? type, bool? enabled
});




}
/// @nodoc
class _$UpdateRuleRequestCopyWithImpl<$Res>
    implements $UpdateRuleRequestCopyWith<$Res> {
  _$UpdateRuleRequestCopyWithImpl(this._self, this._then);

  final UpdateRuleRequest _self;
  final $Res Function(UpdateRuleRequest) _then;

/// Create a copy of UpdateRuleRequest
/// with the given fields replaced by the non-null parameter values.
@pragma('vm:prefer-inline') @override $Res call({Object? domain = freezed,Object? target = freezed,Object? type = freezed,Object? enabled = freezed,}) {
  return _then(_self.copyWith(
domain: freezed == domain ? _self.domain : domain // ignore: cast_nullable_to_non_nullable
as String?,target: freezed == target ? _self.target : target // ignore: cast_nullable_to_non_nullable
as String?,type: freezed == type ? _self.type : type // ignore: cast_nullable_to_non_nullable
as String?,enabled: freezed == enabled ? _self.enabled : enabled // ignore: cast_nullable_to_non_nullable
as bool?,
  ));
}

}


/// Adds pattern-matching-related methods to [UpdateRuleRequest].
extension UpdateRuleRequestPatterns on UpdateRuleRequest {
/// A variant of `map` that fallback to returning `orElse`.
///
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case final Subclass value:
///     return ...;
///   case _:
///     return orElse();
/// }
/// ```

@optionalTypeArgs TResult maybeMap<TResult extends Object?>(TResult Function( _UpdateRuleRequest value)?  $default,{required TResult orElse(),}){
final _that = this;
switch (_that) {
case _UpdateRuleRequest() when $default != null:
return $default(_that);case _:
  return orElse();

}
}
/// A `switch`-like method, using callbacks.
///
/// Callbacks receives the raw object, upcasted.
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case final Subclass value:
///     return ...;
///   case final Subclass2 value:
///     return ...;
/// }
/// ```

@optionalTypeArgs TResult map<TResult extends Object?>(TResult Function( _UpdateRuleRequest value)  $default,){
final _that = this;
switch (_that) {
case _UpdateRuleRequest():
return $default(_that);case _:
  throw StateError('Unexpected subclass');

}
}
/// A variant of `map` that fallback to returning `null`.
///
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case final Subclass value:
///     return ...;
///   case _:
///     return null;
/// }
/// ```

@optionalTypeArgs TResult? mapOrNull<TResult extends Object?>(TResult? Function( _UpdateRuleRequest value)?  $default,){
final _that = this;
switch (_that) {
case _UpdateRuleRequest() when $default != null:
return $default(_that);case _:
  return null;

}
}
/// A variant of `when` that fallback to an `orElse` callback.
///
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case Subclass(:final field):
///     return ...;
///   case _:
///     return orElse();
/// }
/// ```

@optionalTypeArgs TResult maybeWhen<TResult extends Object?>(TResult Function( String? domain,  String? target,  String? type,  bool? enabled)?  $default,{required TResult orElse(),}) {final _that = this;
switch (_that) {
case _UpdateRuleRequest() when $default != null:
return $default(_that.domain,_that.target,_that.type,_that.enabled);case _:
  return orElse();

}
}
/// A `switch`-like method, using callbacks.
///
/// As opposed to `map`, this offers destructuring.
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case Subclass(:final field):
///     return ...;
///   case Subclass2(:final field2):
///     return ...;
/// }
/// ```

@optionalTypeArgs TResult when<TResult extends Object?>(TResult Function( String? domain,  String? target,  String? type,  bool? enabled)  $default,) {final _that = this;
switch (_that) {
case _UpdateRuleRequest():
return $default(_that.domain,_that.target,_that.type,_that.enabled);case _:
  throw StateError('Unexpected subclass');

}
}
/// A variant of `when` that fallback to returning `null`
///
/// It is equivalent to doing:
/// ```dart
/// switch (sealedClass) {
///   case Subclass(:final field):
///     return ...;
///   case _:
///     return null;
/// }
/// ```

@optionalTypeArgs TResult? whenOrNull<TResult extends Object?>(TResult? Function( String? domain,  String? target,  String? type,  bool? enabled)?  $default,) {final _that = this;
switch (_that) {
case _UpdateRuleRequest() when $default != null:
return $default(_that.domain,_that.target,_that.type,_that.enabled);case _:
  return null;

}
}

}

/// @nodoc
@JsonSerializable()

class _UpdateRuleRequest implements UpdateRuleRequest {
  const _UpdateRuleRequest({this.domain, this.target, this.type, this.enabled});
  factory _UpdateRuleRequest.fromJson(Map<String, dynamic> json) => _$UpdateRuleRequestFromJson(json);

@override final  String? domain;
@override final  String? target;
@override final  String? type;
@override final  bool? enabled;

/// Create a copy of UpdateRuleRequest
/// with the given fields replaced by the non-null parameter values.
@override @JsonKey(includeFromJson: false, includeToJson: false)
@pragma('vm:prefer-inline')
_$UpdateRuleRequestCopyWith<_UpdateRuleRequest> get copyWith => __$UpdateRuleRequestCopyWithImpl<_UpdateRuleRequest>(this, _$identity);

@override
Map<String, dynamic> toJson() {
  return _$UpdateRuleRequestToJson(this, );
}

@override
bool operator ==(Object other) {
  return identical(this, other) || (other.runtimeType == runtimeType&&other is _UpdateRuleRequest&&(identical(other.domain, domain) || other.domain == domain)&&(identical(other.target, target) || other.target == target)&&(identical(other.type, type) || other.type == type)&&(identical(other.enabled, enabled) || other.enabled == enabled));
}

@JsonKey(includeFromJson: false, includeToJson: false)
@override
int get hashCode => Object.hash(runtimeType,domain,target,type,enabled);

@override
String toString() {
  return 'UpdateRuleRequest(domain: $domain, target: $target, type: $type, enabled: $enabled)';
}


}

/// @nodoc
abstract mixin class _$UpdateRuleRequestCopyWith<$Res> implements $UpdateRuleRequestCopyWith<$Res> {
  factory _$UpdateRuleRequestCopyWith(_UpdateRuleRequest value, $Res Function(_UpdateRuleRequest) _then) = __$UpdateRuleRequestCopyWithImpl;
@override @useResult
$Res call({
 String? domain, String? target, String? type, bool? enabled
});




}
/// @nodoc
class __$UpdateRuleRequestCopyWithImpl<$Res>
    implements _$UpdateRuleRequestCopyWith<$Res> {
  __$UpdateRuleRequestCopyWithImpl(this._self, this._then);

  final _UpdateRuleRequest _self;
  final $Res Function(_UpdateRuleRequest) _then;

/// Create a copy of UpdateRuleRequest
/// with the given fields replaced by the non-null parameter values.
@override @pragma('vm:prefer-inline') $Res call({Object? domain = freezed,Object? target = freezed,Object? type = freezed,Object? enabled = freezed,}) {
  return _then(_UpdateRuleRequest(
domain: freezed == domain ? _self.domain : domain // ignore: cast_nullable_to_non_nullable
as String?,target: freezed == target ? _self.target : target // ignore: cast_nullable_to_non_nullable
as String?,type: freezed == type ? _self.type : type // ignore: cast_nullable_to_non_nullable
as String?,enabled: freezed == enabled ? _self.enabled : enabled // ignore: cast_nullable_to_non_nullable
as bool?,
  ));
}


}

// dart format on
