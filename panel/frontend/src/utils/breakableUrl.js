import { softBreakParts } from './softBreak.js'

/**
 * Split a hostname/URL into segments that end at wrap-friendly punctuation
 * (`.`, `/`, `:` and similar). Render as text with `<wbr>` between segments;
 * never via v-html, so the value stays escaped.
 *
 * @param {unknown} value
 * @returns {string[]}
 */
export function breakableUrl(value) {
  return softBreakParts(value)
}
