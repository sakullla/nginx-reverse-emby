// Pure JSON-predicate evaluation and JSON-pointer helpers for the declarative
// plugin config renderer. These operate on data only; they never evaluate
// plugin-provided strings as code.

function tokens(pointer) {
  return String(pointer || '')
    .split('/')
    .slice(1)
    .map((part) => part.replaceAll('~1', '/').replaceAll('~0', '~'))
}

export function resolvePointer(model, pointer) {
  if (typeof pointer !== 'string' || !pointer.startsWith('/')) return undefined
  let current = model
  for (const token of tokens(pointer)) {
    if (current == null || typeof current !== 'object' || !Object.hasOwn(current, token)) return undefined
    current = current[token]
  }
  return current
}

function setOwnValue(target, key, value) {
  // Define the legacy accessor names as ordinary own data properties before
  // assigning through Vue's reactive proxy. This preserves JSON semantics
  // without invoking Object.prototype.__proto__ or inherited constructors.
  if (!Object.hasOwn(target, key) && ['__proto__', 'prototype', 'constructor'].includes(key)) {
    Object.defineProperty(target, key, { value: undefined, writable: true, enumerable: true, configurable: true })
  }
  target[key] = value
}

export function setPointer(model, pointer, value) {
  if (!model || typeof model !== 'object' || typeof pointer !== 'string' || !pointer.startsWith('/')) return false
  const parts = tokens(pointer)
  if (!parts.length) return false
  let current = model
  for (const part of parts.slice(0, -1)) {
    if (!Object.hasOwn(current, part) || !current[part] || typeof current[part] !== 'object') {
      setOwnValue(current, part, {})
    }
    current = current[part]
  }
  setOwnValue(current, parts.at(-1), value)
  return true
}

export function assignOwnJSON(target, source) {
  if (!target || typeof target !== 'object' || !source || typeof source !== 'object') return target
  for (const [key, value] of Object.entries(source)) setOwnValue(target, key, value)
  return target
}

export function isEmptyValue(value) {
  return value === undefined || value === null || value === '' ||
    (Array.isArray(value) && value.length === 0) ||
    (typeof value === 'object' && !Array.isArray(value) && Object.keys(value).length === 0)
}

// `predicate` is the backend-validated `visible_when` object: { field, op, value }.
// `scope` is the object against which `field` resolves (root config for top-level
// components, the current array item for array children).
export function evaluateCondition(predicate, scope = {}) {
  if (!predicate || typeof predicate !== 'object') return true
  const fieldValue = resolvePointer(scope, predicate.field)
  const value = predicate.value
  switch (predicate.op) {
    case 'eq': return fieldValue === value
    case 'neq': return fieldValue !== value
    case 'in': return Array.isArray(value) && value.includes(fieldValue)
    case 'notIn': return Array.isArray(value) && !value.includes(fieldValue)
    case 'empty': return isEmptyValue(fieldValue)
    case 'notEmpty': return !isEmptyValue(fieldValue)
    case 'gt': return fieldValue > value
    case 'gte': return fieldValue >= value
    case 'lt': return fieldValue < value
    case 'lte': return fieldValue <= value
    default: return true
  }
}

// Remove a leaf value identified by a JSON pointer. Array elements are set to
// null rather than spliced so sibling indices stay stable.
export function prunePointer(config, pointer) {
  if (!config || typeof config !== 'object' || typeof pointer !== 'string' || !pointer.startsWith('/')) return
  const segments = tokens(pointer)
  let parent = config
  for (const segment of segments.slice(0, -1)) {
    if (parent == null || typeof parent !== 'object' || !Object.hasOwn(parent, segment)) return
    parent = parent[segment]
  }
  const leaf = segments.at(-1)
  if (parent && typeof parent === 'object' && leaf !== undefined) {
    if (Array.isArray(parent)) {
      if (!/^(0|[1-9]\d*)$/.test(leaf) || Number(leaf) >= parent.length) return
      parent[Number(leaf)] = null
    } else {
      delete parent[leaf]
    }
  }
}

// Walk the declarative component tree against the current config and collect
// the full pointers of every leaf (and hidden array) whose condition is false,
// including descendants of a hidden section. Hidden pointers are pruned from
// the submit payload so stale values are not written back.
export function collectHiddenPointers(components, model) {
  const hidden = []
  const walk = (list, basePointer, scope, ancestorHidden) => {
    for (const component of list || []) {
      if (!component || typeof component !== 'object') continue
      const full = basePointer + (component.binding || '')
      const isHidden = ancestorHidden || !evaluateCondition(component.visible_when, scope)
      if (component.type === 'section' || component.type === 'grid') {
        walk(component.children || [], basePointer, scope, isHidden)
      } else if (component.type === 'array') {
        if (isHidden) {
          if (component.binding) hidden.push(full)
          continue
        }
        const items = resolvePointer(model, full)
        if (Array.isArray(items)) {
          items.forEach((item, index) => walk(component.children || [], `${full}/${index}`, item, false))
        }
      } else if (component.binding && isHidden) {
        hidden.push(full)
      }
    }
  }
  walk(components, '', model, false)
  return hidden
}
