// The SPEC.md parity config, shared by parity.mjs, oracle.mjs and bench.mjs. Importing it
// checks the environment liquidjs renders in, and liquidjs's resolved options.
//
//   node config.mjs -w    rewrite liquidjs-options.json after a deliberate change
import { Context, Liquid } from 'liquidjs';
import { readFileSync, writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

process.env.TZ = 'UTC';

export function newEngine(opts) {
  return new Liquid({ lenientIf: true, ...opts });
}

// countMemory makes every render add what it charges memoryLimit to the returned
// counter's spent, so tests can check mist charges at least as much. renderLimit is a
// Limiter too, charged in milliseconds.
export function countMemory() {
  const counter = { spent: 0 };
  const { memoryLimit } = new Context();
  const Limiter = memoryLimit.constructor;
  const use = Limiter.prototype.use;
  Limiter.prototype.use = function (count) {
    use.call(this, count);
    if (this.message === memoryLimit.message && +count > 0) counter.spent += +count;
  };
  return counter;
}

function fail(msg) {
  console.error(`config.mjs: ${msg}`);
  process.exit(1);
}

// liquidjs's locale option defaults to the process locale, which date names follow.
const { locale, timeZone } = Intl.DateTimeFormat().resolvedOptions();
if (locale !== 'en-US' || timeZone !== 'UTC') fail(`parity needs locale en-US and time zone UTC, got ${locale} and ${timeZone} (try LANG=en_US.UTF-8)`);

const normalise = (v) =>
  typeof v === 'function' ? 'function'
  : v === undefined || (typeof v === 'number' && !isFinite(v)) ? String(v)
  : Array.isArray(v) ? v.map(normalise)
  : v && typeof v === 'object' ? Object.fromEntries(Object.entries(v).map(([k, x]) => [k, normalise(x)]))
  : v;

const snapshot = new URL('liquidjs-options.json', import.meta.url);
const got = normalise(newEngine().options);
if (process.argv[1] === fileURLToPath(import.meta.url) && process.argv[2] === '-w') {
  writeFileSync(snapshot, JSON.stringify(got, null, 2) + '\n');
} else {
  const want = JSON.parse(readFileSync(snapshot, 'utf8'));
  const changed = [...new Set([...Object.keys(want), ...Object.keys(got)])].filter((k) => JSON.stringify(want[k]) !== JSON.stringify(got[k]));
  const show = (o, k) => (k in o ? JSON.stringify(o[k]) : 'absent');
  if (changed.length) fail(`liquidjs options differ from liquidjs-options.json: ${changed.map((k) => `${k} ${show(want, k)} -> ${show(got, k)}`).join(', ')}`);
}
