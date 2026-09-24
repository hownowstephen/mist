// Runs harvested/generated cases through liquidjs (render service config) and
// writes their lax and strict results, the oracle for corpus_test.go.
//
//   node oracle.mjs [-o OUT.json] CASES.json...   (default out: ../testdata/corpus.json)
import { Liquid } from 'liquidjs';
import { readFileSync, writeFileSync } from 'node:fs';

// renderLimit only guards corpus generation against resource-limit specs.
const engine = new Liquid({ lenientIf: true, renderLimit: 2000 });
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
