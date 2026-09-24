// Asserts testdata/cases.json against liquidjs as configured by the render service.
// Cases marked "unsupported" are only printed: mist bails, so liquidjs decides.
import { Liquid } from 'liquidjs';
import { readFileSync } from 'node:fs';

const engine = new Liquid({ lenientIf: true });
const cases = JSON.parse(readFileSync(new URL('../testdata/cases.json', import.meta.url)));
let failed = 0;

for (const c of cases) {
  let got, err;
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
}
console.log(failed ? `${failed} parity failures` : `all ${cases.length} cases agree with liquidjs`);
process.exit(failed ? 1 : 0);
