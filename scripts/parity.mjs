// Asserts testdata/cases.json against liquidjs as configured in SPEC.md.
// Cases marked "unsupported" are only printed: mist bails, so liquidjs decides. A case's
// "mem" must be exactly what its render charges memoryLimit.
import { readFileSync } from 'node:fs';
import { countMemory, newEngine } from './config.mjs';

const engine = newEngine();
const memory = countMemory();
const realNow = Date.now;
const cases = JSON.parse(readFileSync(new URL('../testdata/cases.json', import.meta.url)));
let failed = 0;

for (const c of cases) {
  let got, err;
  Date.now = c.now === undefined ? realNow : () => c.now;
  memory.spent = 0;
  try {
    got = await engine.parseAndRender(c.tpl, structuredClone(c.data ?? {}), { strictVariables: !!c.strict });
  } catch (e) {
    err = e.message.split('\n')[0];
  }
  if (c.err === 'unsupported') {
    if (process.argv.includes('-v')) console.log(`info ${c.name}: ${err ? 'ERR ' + err : JSON.stringify(got)}`);
    continue;
  }
  // mist fails fast on undefined; liquidjs must fail too, but may report a later syntax error.
  const ok = c.err === 'undefined' ? err !== undefined : err === undefined && got === c.out;
  if (!ok) {
    failed++;
    console.log(`FAIL ${c.name}: want ${c.err ?? JSON.stringify(c.out)}, liquidjs gave ${err ? 'ERR ' + err : JSON.stringify(got)}`);
  }
  if (c.mem !== undefined && memory.spent !== c.mem) {
    failed++;
    console.log(`FAIL ${c.name}: want mem ${c.mem}, liquidjs charged ${memory.spent}`);
  }
}
// liquidjs_filters.go must name exactly the filters this liquidjs defines.
const listed = [...readFileSync(new URL('../liquidjs_filters.go', import.meta.url), 'utf8').matchAll(/^\t"([^"]+)": +true,$/gm)].map((m) => m[1]);
const defined = Object.keys(engine.filters).sort();
if (listed.join(' ') !== defined.join(' ')) {
  failed++;
  console.log(`FAIL liquidjs_filters.go lists ${listed.length} filters; liquidjs defines ${defined.length}: ${defined.join(' ')}`);
}
console.log(failed ? `${failed} parity failures` : `all ${cases.length} cases agree with liquidjs`);
process.exit(failed ? 1 : 0);
