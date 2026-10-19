// liquidjs for FuzzParity (parity_test.go), kept running across fuzz inputs: each stdin
// line is a JSON {tpl, data, strict}, answered by a stdout line {out} or {err}.
import { createInterface } from 'node:readline';
import { oracleEngine } from './config.mjs';

const engine = oracleEngine();
for await (const line of createInterface({ input: process.stdin, crlfDelay: Infinity })) {
  const { tpl, data, strict } = JSON.parse(line);
  let res;
  try {
    res = { out: engine.parseAndRenderSync(tpl, data, { strictVariables: strict }) };
  } catch (e) {
    res = { err: String(e.message).split('\n')[0].slice(0, 200) };
  }
  process.stdout.write(JSON.stringify(res) + '\n');
}
