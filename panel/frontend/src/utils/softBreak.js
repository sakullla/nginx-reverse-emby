const BREAK_AFTER = new Set(['.', '/', '-', '_', '?', '&', '=', ':', '@'])

// Split text so long hostnames and URLs can wrap after punctuation
// instead of breaking in the middle of a word.
export function softBreakParts(value) {
  const text = String(value ?? '')
  if (!text) return []
  const parts = []
  let buf = ''
  for (const ch of text) {
    buf += ch
    if (BREAK_AFTER.has(ch)) {
      parts.push(buf)
      buf = ''
    }
  }
  if (buf) parts.push(buf)
  return parts
}
