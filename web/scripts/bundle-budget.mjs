// Initial-JS budget (docs/architecture/frontend.md#technology): for every route, the gzip size
// of the JavaScript the browser loads before the page renders. That is the shell's
// modulepreloads (build/index.html) plus the route's SvelteKit nodes (root layout, section
// layouts, page) and their static imports. Dynamic imports (pdf.js, anything lazy) are not
// followed. Fails when any route exceeds the budget. No dependencies; run after `npm run build`.
import { readFileSync, readdirSync } from 'node:fs';
import process from 'node:process';
import { gzipSync } from 'node:zlib';
import { dirname, join, resolve } from 'node:path';

const budget = 300 * 1024;
const build = resolve(import.meta.dirname, '..', 'build');
const root = join(build, '_app/immutable');

const read = (file) => readFileSync(file, 'utf8');
const gzipSize = (file) => gzipSync(readFileSync(file), { level: 9 }).length;
const fail = (msg) => {
	console.error(`bundle-budget: ${msg}`);
	process.exit(1);
};

// Static imports only: `import"x.js"`, `import{a}from"x.js"`, `export{a}from"x.js"`. A dynamic
// `import(` has a parenthesis, which the clause pattern excludes.
const staticImport = /\b(?:import|export)\s*(?:[^'"`()]*?\bfrom\s*)?["'`]([^"'`]+\.js)["'`]/g;
function closure(entry, seen = new Set()) {
	if (seen.has(entry)) return seen;
	seen.add(entry);
	for (const m of read(entry).matchAll(staticImport)) closure(resolve(dirname(entry), m[1]), seen);
	return seen;
}

const html = read(join(build, 'index.html'));
const shell = [...html.matchAll(/<link href="\/(_app\/immutable\/[^"]+\.js)" rel="modulepreload">/g)].map((m) => join(build, m[1]));
if (shell.length === 0) fail('no modulepreload links in build/index.html; has the build layout changed?');

const entries = readdirSync(join(root, 'entry'));
const appFile = entries.find((f) => f.startsWith('app.'));
if (!appFile) fail('entry/app.*.js not found');
const app = read(join(root, 'entry', appFile));
const alias = app.match(/\b(\w+) as dictionary\b/)?.[1];
const dict = alias && app.match(new RegExp(`[,;\\s]${alias}=(\\{[^}]*\\})`))?.[1];
if (!dict) fail('route dictionary not found in entry/app.js; has the SvelteKit output changed?');
const routes = JSON.parse(dict); // "/(app)/data": [leafNode, [layoutNodes]]

const nodeFiles = readdirSync(join(root, 'nodes'));
const node = (n) => {
	const f = nodeFiles.find((x) => x.startsWith(`${n}.`));
	if (!f) fail(`node ${n} not found`);
	return join(root, 'nodes', f);
};

const base = new Set();
for (const f of [...shell, node(0)]) closure(f, base);
const sizes = new Map();
const size = (f) => sizes.get(f) ?? (sizes.set(f, gzipSize(f)), sizes.get(f));
const total = (files) => [...files].reduce((n, f) => n + size(f), 0);

const rows = Object.entries(routes).map(([route, [leaf, layouts = []]]) => {
	const files = new Set(base);
	for (const n of [...layouts, leaf]) closure(node(n), files);
	return { route, bytes: total(files), files: files.size };
});
rows.sort((a, b) => b.bytes - a.bytes);

const kib = (n) => (n / 1024).toFixed(1).padStart(6);
console.log(`Initial JS per route (gzip, level 9); budget ${budget / 1024} KiB`);
console.log(`  shell + root layout: ${kib(total(base))} KiB, ${base.size} files`);
for (const r of rows) console.log(`  ${kib(r.bytes)} KiB  ${String(r.files).padStart(2)} files  ${r.route}`);

const initial = new Set();
const rowFiles = Object.entries(routes).flatMap(([, [leaf, layouts = []]]) => [...layouts, leaf]);
for (const f of [...base, ...rowFiles.map(node)]) closure(f, initial);
const lazy = ['entry', 'chunks', 'nodes']
	.flatMap((d) => readdirSync(join(root, d)).map((f) => join(root, d, f)))
	.filter((f) => f.endsWith('.js') && !initial.has(f))
	.reduce((n, f) => n + size(f), 0);
console.log(`  lazy chunks (on no initial path): ${kib(lazy)} KiB`);

const worst = rows[0];
if (worst.bytes > budget) fail(`${worst.route} is ${kib(worst.bytes).trim()} KiB gzip, over ${budget / 1024} KiB`);
console.log(`ok: largest route ${kib(worst.bytes).trim()} KiB of ${budget / 1024} KiB`);
