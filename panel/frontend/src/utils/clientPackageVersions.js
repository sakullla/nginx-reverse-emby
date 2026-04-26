function parseClientPackageVersion(value) {
  const version = { core: [0, 0, 0], prerelease: [] }
  const coreAndPrerelease = String(value || '').trim().split('+', 1)[0]
  const hyphenIndex = coreAndPrerelease.indexOf('-')
  const core = hyphenIndex >= 0 ? coreAndPrerelease.slice(0, hyphenIndex) : coreAndPrerelease
  const prerelease = hyphenIndex >= 0 ? coreAndPrerelease.slice(hyphenIndex + 1) : ''
  core.split('.').slice(0, 3).forEach((part, index) => {
    const number = Number.parseInt(part, 10)
    if (!Number.isNaN(number)) version.core[index] = number
  })
  if (prerelease) version.prerelease = prerelease.split('.')
  return version
}

function parseNumericIdentifier(value) {
  if (!/^[0-9]+$/.test(value)) return { numeric: false, value }
  return { numeric: true, value: Number.parseInt(value, 10) }
}

function comparePrereleaseIdentifier(left, right) {
  const leftParsed = parseNumericIdentifier(left)
  const rightParsed = parseNumericIdentifier(right)
  if (leftParsed.numeric && rightParsed.numeric) return Math.sign(leftParsed.value - rightParsed.value)
  if (leftParsed.numeric) return -1
  if (rightParsed.numeric) return 1
  return Math.sign(left.localeCompare(right))
}

function comparePrerelease(left, right) {
  const length = Math.max(left.length, right.length)
  for (let i = 0; i < length; i += 1) {
    if (i >= left.length) return -1
    if (i >= right.length) return 1
    const result = comparePrereleaseIdentifier(left[i], right[i])
    if (result !== 0) return result
  }
  return 0
}

export function compareClientPackageVersions(left, right) {
  const leftVersion = parseClientPackageVersion(left)
  const rightVersion = parseClientPackageVersion(right)
  for (let i = 0; i < 3; i += 1) {
    if (leftVersion.core[i] > rightVersion.core[i]) return 1
    if (leftVersion.core[i] < rightVersion.core[i]) return -1
  }
  const leftHasPrerelease = leftVersion.prerelease.length > 0
  const rightHasPrerelease = rightVersion.prerelease.length > 0
  if (!leftHasPrerelease && rightHasPrerelease) return 1
  if (leftHasPrerelease && !rightHasPrerelease) return -1
  if (leftHasPrerelease && rightHasPrerelease) {
    return comparePrerelease(leftVersion.prerelease, rightVersion.prerelease)
  }
  return 0
}
