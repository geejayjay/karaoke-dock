// Saves the letter shapes of "dock" in the built-in script font, so pages can
// trace its edges without loading a font reader. Run after changing the font:
//   node scripts/dock-outline.mjs
// With a font path and an output path, it writes those instead.
import { readFileSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import opentype from 'opentype.js'

const { parse } = opentype

const here = (p) => fileURLToPath(new URL(p, import.meta.url))
const fontPath = process.argv[2] ?? here('../node_modules/@fontsource/herr-von-muellerhoff/files/herr-von-muellerhoff-latin-400-normal.woff')
const outPath = process.argv[3] ?? here('../src/shared/dock-outline.json')

const raw = readFileSync(fontPath)
const font = parse(raw.buffer.slice(raw.byteOffset, raw.byteOffset + raw.byteLength))
const em = font.unitsPerEm
// One unit per font unit, baseline at y=0, like the server's outline.
const d = font.getPath('dock', 0, 0, em).toPathData(1)
writeFileSync(outPath, JSON.stringify({ em, d }) + '\n')
console.log(`wrote ${outPath} (${d.length} characters)`)
