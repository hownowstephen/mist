// Benchmarks liquidjs on testdata/bench, the fixtures mist's BenchmarkFixture renders, and
// compares the two.
//
//   node bench.mjs -w              write each fixture's liquidjs output to NAME.out
//   node bench.mjs -check          check liquidjs still renders every NAME.out
//   node bench.mjs [-go GO.txt]    time liquidjs; with `go test -bench Fixture` output, compare
import { Liquid } from 'liquidjs';
import { readFileSync, readdirSync, writeFileSync, appendFileSync } from 'node:fs';

// The SPEC parity config, with the clock BenchmarkFixture uses.
process.env.TZ = 'UTC';
Date.now = () => 1700000000123;

// mist must stay at least this many times faster than liquidjs rendering a pre-parsed template.
const minSpeedup = 4;

const dir = new URL('../testdata/bench/', import.meta.url);
const engine = new Liquid({ lenientIf: true });
const opts = { strictVariables: true };
const fixtures = readdirSync(dir).filter((f) => f.endsWith('.liquid')).sort().map((f) => {
  const name = f.slice(0, -'.liquid'.length);
  return { name, tpl: readFileSync(new URL(f, dir), 'utf8'), data: JSON.parse(readFileSync(new URL(name + '.json', dir), 'utf8')) };
});
const args = process.argv.slice(2);

if (args[0] === '-w' || args[0] === '-check') {
  let failed = 0;
  for (const { name, tpl, data } of fixtures) {
    const out = engine.parseAndRenderSync(tpl, data, opts);
    if (args[0] === '-w') writeFileSync(new URL(name + '.out', dir), out);
    else if (out !== readFileSync(new URL(name + '.out', dir), 'utf8')) failed++, console.log(`FAIL ${name}: liquidjs output differs from ${name}.out`);
  }
  console.log(failed ? `${failed} fixtures differ` : `${fixtures.length} fixtures agree with liquidjs`);
  process.exit(failed ? 1 : 0);
}

// nsPerOp runs fn for about 200ms, five times, and returns the median ns/op.
function nsPerOp(fn) {
  for (let i = 0; i < 200; i++) fn();
  const runs = [];
  for (let r = 0; r < 5; r++) {
    let n = 0;
    const start = process.hrtime.bigint();
    let elapsed = 0n;
    while (elapsed < 200_000_000n) {
      for (let i = 0; i < 100; i++) fn();
      n += 100;
      elapsed = process.hrtime.bigint() - start;
    }
    runs.push(Number(elapsed) / n);
  }
  return runs.sort((a, b) => a - b)[2];
}

// goNsPerOp is the median ns/op per fixture in `go test -bench Fixture` output.
function goNsPerOp(file) {
  const by = {};
  for (const m of readFileSync(file, 'utf8').matchAll(/^BenchmarkFixture\/(\S+?)(?:-\d+)?\s+\d+\s+([\d.]+) ns\/op/gm)) (by[m[1]] ??= []).push(+m[2]);
  return Object.fromEntries(Object.entries(by).map(([k, v]) => [k, v.sort((a, b) => a - b)[v.length >> 1]]));
}

const goIdx = args.indexOf('-go');
const mist = goIdx >= 0 ? goNsPerOp(args[goIdx + 1]) : {};
const fmt = (ns) => (ns >= 1000 ? `${(ns / 1000).toFixed(2)} µs` : `${ns.toFixed(0)} ns`);
const rows = [`liquidjs ${JSON.parse(readFileSync(new URL('node_modules/liquidjs/package.json', import.meta.url))).version}, Node ${process.versions.node}`, '',
  '| fixture | mist | liquidjs parse+render | liquidjs pre-parsed |', '|---|---|---|---|'];
const warnings = [];
for (const { name, tpl, data } of fixtures) {
  const parsed = engine.parse(tpl);
  const full = nsPerOp(() => engine.parseAndRenderSync(tpl, data, opts));
  const pre = nsPerOp(() => engine.renderSync(parsed, data, opts));
  const m = mist[name];
  const vs = (ns) => (m ? `${fmt(ns)} (${(ns / m).toFixed(1)}×)` : fmt(ns));
  rows.push(`| ${name} | ${m ? fmt(m) : '–'} | ${vs(full)} | ${vs(pre)} |`);
  if (m && pre / m < minSpeedup) warnings.push(`::warning::mist is only ${(pre / m).toFixed(1)}× faster than pre-parsed liquidjs on ${name} (floor ${minSpeedup}×)`);
}
console.log(rows.join('\n'));
if (process.env.GITHUB_STEP_SUMMARY) appendFileSync(process.env.GITHUB_STEP_SUMMARY, '## mist vs liquidjs\n\n' + rows.join('\n') + '\n');
for (const w of warnings) console.log(w);
