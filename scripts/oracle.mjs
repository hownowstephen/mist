// Runs harvested/generated cases through liquidjs (the SPEC.md parity config) and
// writes their lax and strict results, and what the lax render charged memoryLimit,
// the oracle for corpus_test.go.
//
//   node oracle.mjs [-o OUT.json] CASES.json...   (default out: ../testdata/corpus.json)
import { readFileSync, writeFileSync } from 'node:fs';
import { countMemory, oracleEngine } from './config.mjs';

const memory = countMemory();
const engine = oracleEngine();

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
  memory.spent = 0;
  const lax = run(tpl, data, false);
  const mem = memory.spent;
  const strict = run(tpl, data, true);
  const c = { src, name, tpl, data, lax };
  if (lax.out !== undefined && mem > 0) c.mem = mem;
  if (JSON.stringify(strict) !== JSON.stringify(lax)) c.strict = strict;
  return c;
});
writeFileSync(out, '[\n' + corpus.map((c) => JSON.stringify(c)).join(',\n') + '\n]\n');
console.log(`wrote ${corpus.length} cases`);
