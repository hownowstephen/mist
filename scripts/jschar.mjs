// Writes testdata/jschar.json: the code points in each character class mist mirrors, as
// [first, last] ranges, and liquidjs's output where each whitespace character meets each
// place mist treats whitespace, for checking space.go and the filters that use it.
//
//   space            JavaScript's \s
//   trim             what {{- trims, found by rendering it after each character
//   cjk              what number_of_words: 'cjk' counts as a word of its own
//   slugify MODE     what slugify keeps, rather than replacing with -
//
//   node jschar.mjs [-o OUT.json]
import { writeFileSync } from 'node:fs';
import { newEngine } from './config.mjs';

const engine = newEngine();
const chars = [];
for (let cp = 0; cp <= 0x10ffff; cp++) if (cp < 0xd800 || cp > 0xdfff) chars.push(String.fromCodePoint(cp));
const chunks = [];
for (let i = 0; i < chars.length; i += 4096) chunks.push(chars.slice(i, i + 4096));

const ranges = (has) => {
  const rs = [];
  for (const c of chars.filter(has).map((c) => c.codePointAt(0))) {
    if (rs.length && rs.at(-1)[1] === c - 1) rs.at(-1)[1] = c;
    else rs.push([c, c]);
  }
  return rs;
};

// '{' can't come before {{- as text, and isn't blank anyway.
const trimmed = new Set();
for (const cs of chunks) {
  const out = engine.parseAndRenderSync(cs.map((c) => (c === '{' ? '' : '|' + c + '{{- 1 }}')).join(''));
  let p = 0;
  for (const c of cs) {
    if (c === '{') continue;
    if (out.startsWith('|' + c + '1', p)) p += c.length + 2;
    else if (out.startsWith('|1', p)) (trimmed.add(c), (p += 2));
    else throw new Error(`unexpected output for U+${c.codePointAt(0).toString(16)}`);
  }
}

// filter's output for "a" + c + "a", for each character.
const perItem = (filter) =>
  chunks.flatMap((cs) =>
    JSON.parse('[' + engine.parseAndRenderSync(`{% for s in cs %}{% unless forloop.first %},{% endunless %}{{ s | ${filter} | json }}{% endfor %}`, { cs: cs.map((c) => 'a' + c + 'a') }) + ']'),
  );
const words = perItem(`number_of_words: 'cjk'`);
const classes = {
  space: ranges((c) => /\s/.test(c)),
  trim: ranges((c) => trimmed.has(c)),
  cjk: ranges((c, i) => words[i] === 3),
};
for (const mode of ['default', 'pretty', 'raw']) {
  const out = perItem(`slugify: '${mode}', true`);
  classes['slugify ' + mode] = ranges((c, i) => out[i] !== 'a-a');
}

// Each whitespace character, in the template or the data, wherever mist tests for whitespace
// beyond ASCII. (Between tokens mist reads only ASCII whitespace and U+00A0, and bails on the rest.)
const blanks = chars.filter((c) => /\s/.test(c) || trimmed.has(c));
const sites = [
  (c) => ['|' + c + '{{- 1 -}}' + c + '|'],
  (c) => ['{%- if true -%}' + c + 'x' + c + '{%- endif -%}'],
  (c) => ['{% # a\n' + c + '# b %}x'],
  (c) => ['{% liquid\n' + c + 'echo 1' + c + '\n%}'],
  (c) => ['{{ s | strip }}|{{ s | lstrip }}|{{ s | rstrip }}', { s: c + 'x' + c }],
  (c) => ['{{ s | normalize_whitespace }}|{{ s | number_of_words }}|{{ s | truncatewords: 1 }}', { s: c + 'a' + c + c + 'b' + c }],
  (c) => ['{{ s | slugify: "raw" }}|{{ s | to_integer }}', { s: c + '1' + c }],
  (c) => ['{% if s == blank %}y{% endif %}{% if s == empty %}e{% endif %}', { s: c }],
];
const cases = blanks.flatMap((c) =>
  sites.map((site) => {
    const [tpl, data = {}] = site(c);
    let out = null;
    try {
      out = engine.parseAndRenderSync(tpl, data);
    } catch {}
    return { tpl, data, out };
  }),
);

const dest = process.argv[2] === '-o' ? process.argv[3] : new URL('../testdata/jschar.json', import.meta.url);
writeFileSync(dest, JSON.stringify({ node: process.version, classes, cases }) + '\n');
console.log(Object.entries(classes).map(([k, v]) => `${k} ${v.length}`).join(', ') + `; ${cases.length} cases`);
