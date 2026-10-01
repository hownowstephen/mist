// Runs harvested/generated cases through liquidjs (the SPEC.md parity config) and
// writes their lax and strict results, the oracle for corpus_test.go.
//
//   node oracle.mjs [-o OUT.json] CASES.json...   (default out: ../testdata/corpus.json)
import { Liquid } from 'liquidjs';
import { readFileSync, writeFileSync } from 'node:fs';

// The SPEC parity config runs with TZ=UTC. A fixed clock keeps 'now' reproducible;
// corpus_test.go's corpusNow must match.
process.env.TZ = 'UTC';
Date.now = () => 1700000000123;

// renderLimit only guards corpus generation against resource-limit specs.
const engine = new Liquid({ lenientIf: true, renderLimit: 2000 });

// liquidjs's strip_html loops forever on an unclosed '<' after text, where mist bails;
// throw there instead so generated cases can't hang the oracle.
const stripHTML = engine.filters.strip_html;
engine.registerFilter('strip_html', function (v) {
  const s = Array.isArray(v) ? v.flat(Infinity).join('') : String(v ?? '');
  const blocks = new Map([['<script', '</script>'], ['<style', '</style>'], ['<!--', '-->'], ['<', '>']]);
  for (let i = 0; i < s.length; ) {
    const lt = s.indexOf('<', i);
    if (lt < 0) break;
    let next = i;
    for (const [open, close] of blocks) {
      if (!s.startsWith(open, lt)) continue;
      const e = s.indexOf(close, lt + open.length);
      if (e >= 0) { next = e + close.length; break; }
      blocks.delete(open);
    }
    if (next !== i) i = next;
    else if (i === lt) break;
    else throw new Error('strip_html would loop forever');
  }
  return stripHTML.call(this, v);
});
const args = process.argv.slice(2);
let out = new URL('../testdata/corpus.json', import.meta.url);
if (args[0] === '-o') [, out] = args.splice(0, 2);
const cases = args.flatMap((f) => JSON.parse(readFileSync(f, 'utf8')));

function run(tpl, data, strict) {
  try {
    return { out: engine.parseAndRenderSync(tpl, JSON.parse(JSON.stringify(data)), { strictVariables: strict }) };
  } catch (e) {
    return { err: String(e.message).split('\n')[0].slice(0, 200) };
  }
}

const corpus = cases.map(({ src, name, tpl, data }) => {
  const lax = run(tpl, data, false);
  const strict = run(tpl, data, true);
  const c = { src, name, tpl, data, lax };
  if (JSON.stringify(strict) !== JSON.stringify(lax)) c.strict = strict;
  return c;
});
writeFileSync(out, '[\n' + corpus.map((c) => JSON.stringify(c)).join(',\n') + '\n]\n');
console.log(`wrote ${corpus.length} cases`);
