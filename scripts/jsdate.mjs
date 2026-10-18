// Writes testdata/jsdate.json: liquidjs's date filter for every strftime directive across
// a spread of instants, and for edge-case input strings, for checking mist's date filter.
// An output of null means liquidjs threw.
//
//   node jsdate.mjs [-o OUT.json]
import { writeFileSync } from 'node:fs';
import { newEngine } from './config.mjs';

const engine = newEngine();
const tpl = engine.parse('{{ d | date: f }}');
const render = (d, f) => {
  try {
    return engine.renderSync(tpl, { d, f });
  } catch {
    return null;
  }
};

const utc = (y, mo, d, h = 0, mi = 0, s = 0, ms = 0) => {
  const t = new Date(0);
  t.setUTCFullYear(y, mo - 1, d); // Date.UTC maps years 0-99 to 1900-1999
  t.setUTCHours(h, mi, s, ms);
  return t.getTime();
};
const ms = [
  ...[0, 11, 12, 13, 23].map((h) => utc(2024, 1, 15, h, 7, 9)),
  ...[3, 4, 5, 6, 7, 8, 9].map((d) => utc(2024, 3, d, 12, 34, 56, 789)), // Sunday to Saturday
  ...[2017, 2018, 2019, 2020, 2015, 2016, 2022].flatMap((y) => [utc(y, 1, 1), utc(y, 1, 7, 23, 59, 59), utc(y, 12, 31, 12)]), // Jan 1 on each weekday
  ...[1, 2, 3, 11, 12, 13, 21, 22, 23, 31].map((d) => utc(2024, 5, d, 6)),
  utc(2024, 2, 29), utc(2000, 2, 29, 23, 59, 59, 999), utc(1600, 2, 29), utc(2023, 3, 1), utc(1900, 3, 1), utc(2100, 2, 28),
  utc(1000, 1, 1), utc(1000, 1, 1, 12, 0, 0, 1), utc(9999, 12, 31, 23, 59, 59, 999), utc(999, 12, 31, 23, 59, 59), utc(10000, 1, 1),
  0, -500, 1000000000500, 1000000000499, 1700000000123,
];
const instants = ms.map((t) => t / 1000); // the date filter reads numbers as seconds

const letters = [...'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ'];
const formats = [
  ...[...letters, '%'].map((c) => '%' + c),
  ...[...'aAbBCdeHIjklLmMNpPqsSuUwWyYz'].flatMap((c) => ['-', '_', '0', '^', '#', ':', '1', '10', 'E', 'O'].map((f) => '%' + f + c)),
];
const out = {};
for (const f of formats) out[f] = instants.map((d) => render(d, f));

// Each renders with a format that shows every field and the input's offset.
const inputFormat = '%Y-%m-%dT%H:%M:%S.%L %z';
const inputStrings = [
  '2024-01-15T24:00:00Z', '2024-01-15T24:00Z', '2024-01-15T24:00:00.000Z', '2024-01-15T24:00:01Z', '2024-01-15T24:30:00Z', '2024-01-15T25:00:00Z', '2024-01-15 24:00',
  '2024-01-15T12:00:59Z', '2024-01-15T12:00:60Z', '2024-01-15T12:00:61Z', '2024-01-15T12:60:00Z', '2024-01-15T12:59:59.999Z',
  '2024-02-29', '2023-02-29', '2024-02-30', '2024-02-30T00:00:00Z', '2024-04-31', '2024-13-01', '2024-00-10', '2024-01-00', '2024-01-32',
  '2024-01-15T12:00:00+24:00', '2024-01-15T12:00:00-24:00', '2024-01-15T12:00:00+23:59', '2024-01-15T12:00:00-23:59', '2024-01-15T12:00:00+24:01',
  '2024-01-15T12:00:00+2400', '2024-01-15T12:00:00+00:60', '2024-01-15T12:00+24:00', '2024-01-15T12:00:00.123456789Z', '2024-01-15T12:00:00.1234567891Z',
  '24-01-15', '1/15/24', '01/15/99', 'Jan 15 24', '15 Jan 24', 'Jan 1 49', 'Jan 1 50', 'Jan 1, 0024', '2/30/2024', 'Feb 30 2024', '30 Feb 2024', '2024/2/30',
  'Jan 15 2024 24:00', 'Jan 15 2024 24:00:00', 'Jan 15 2024 25:00', 'Jan 15 2024 12:00:60', '1/15/2024 24:00', 'Jan 15 2024 12:60',
  'Jan 15 2024 12:00 AM', 'Jan 15 2024 12:00 PM', 'Jan 15 2024 0:00 AM', 'Jan 15 2024 13:00 PM', 'Jan 15 2024 11:59:59 PM',
  'Jan 15 2024 12:00 +2400', 'Jan 15 2024 12:00 -2400', 'Jan 15 2024 12:00 +2359', 'Jan 15 2024 12:00 GMT+2400', 'Jan 15 2024 12:00 UTC-2359',
  '1000-01-01', '0999-12-31', '9999-12-31T23:59:59.999Z', '10000-01-01', '+010000-01-01T00:00:00Z', 'Jan 1 1000', 'Dec 31 9999 23:59:59',
];
const inputs = Object.fromEntries(inputStrings.map((s) => [s, render(s, inputFormat)]));

const dest = process.argv[2] === '-o' ? process.argv[3] : new URL('../testdata/jsdate.json', import.meta.url);
writeFileSync(dest, JSON.stringify({ node: process.version, instants, formats: out, inputFormat, inputs }) + '\n');
console.log(`${formats.length} formats × ${instants.length} instants, ${inputStrings.length} inputs`);
