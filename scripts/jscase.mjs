// Writes testdata/jscase.json: every code point whose JavaScript toUpperCase or
// toLowerCase differs from itself, for checking mist's case mapping (capitalize).
//
//   node jscase.mjs
import { writeFileSync } from 'node:fs';

const upper = {}, lower = {};
for (let cp = 0; cp <= 0x10ffff; cp++) {
  if (cp >= 0xd800 && cp <= 0xdfff) continue;
  const s = String.fromCodePoint(cp);
  if (s.toUpperCase() !== s) upper[cp] = s.toUpperCase();
  if (s.toLowerCase() !== s) lower[cp] = s.toLowerCase();
}
writeFileSync(new URL('../testdata/jscase.json', import.meta.url), JSON.stringify({ node: process.version, upper, lower }) + '\n');
console.log(`upper ${Object.keys(upper).length}, lower ${Object.keys(lower).length}`);
