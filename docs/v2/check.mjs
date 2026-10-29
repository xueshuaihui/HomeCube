import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.dirname(fileURLToPath(import.meta.url));
const dir = path.join(root, 'prototypes');
const files = fs.readdirSync(dir).filter(f => f.endsWith('.html'));
let errors = 0;

for (const f of files) {
  let html = fs.readFileSync(path.join(dir, f), 'utf8');
  html = html.replace(/<script[\s\S]*?<\/script>/g, '');
  const problems = [];

  // local href/src targets
  for (const m of html.matchAll(/(?:href|src)="([^"]+)"/g)) {
    const url = m[1];
    if (/^(https?:|#|data:|javascript:)/.test(url) || url === '') continue;
    const clean = url.split('?')[0];
    if (!clean) continue;
    const target = path.resolve(dir, clean);
    if (!fs.existsSync(target)) problems.push(`missing target: ${url}`);
  }

  // duplicate ids
  const ids = [...html.matchAll(/\sid="([^"]+)"/g)].map(m => m[1]);
  const dup = ids.filter((v, i) => ids.indexOf(v) !== i);
  if (dup.length) problems.push(`duplicate id: ${[...new Set(dup)].join(',')}`);

  // sheet targets
  for (const m of html.matchAll(/data-sheet-open="([^"]+)"/g)) {
    if (!ids.includes(m[1])) problems.push(`sheet target missing: ${m[1]}`);
  }

  // hot numbers unique
  const hots = [...html.matchAll(/data-hot="([^"]+)"/g)].map(m => m[1]);
  const duph = hots.filter((v, i) => hots.indexOf(v) !== i);
  if (duph.length) problems.push(`duplicate hot: ${[...new Set(duph)].join(',')}`);

  // states declared vs state= links
  const declared = (html.match(/data-states="([^"]*)"/) || [,''])[1].split(',').filter(Boolean);
  const selfName = f;
  for (const m of html.matchAll(/(?:href|src)="([^"]*state=[^"]*)"/g)) {
    const u = m[1];
    if (!u.includes(selfName)) continue;
    const sv = new URLSearchParams(u.split('?')[1] || '').get('state');
    if (sv && !declared.includes(sv)) problems.push(`undeclared state used in link: ${sv}`);
  }

  // data-state-block used only with declared states
  for (const m of html.matchAll(/data-state-block="([^"]+)"/g)) {
    if (!declared.includes(m[1])) problems.push(`state block not declared: ${m[1]}`);
  }

  // tabbar link consistency
  const tabs = [...html.matchAll(/class="tab[^"]*" href="([^"]+)"/g)].map(m => m[1]);
  if (tabs.length && tabs.length !== 5) problems.push(`tabbar has ${tabs.length} items`);

  if (problems.length) {
    errors += problems.length;
    console.log(`\n[${f}]`);
    problems.forEach(p => console.log('  - ' + p));
  }
}

// ---- screen cards vs prototypes ----
const cardDirs = ['screens/homeos', 'screens/calendar', 'screens/finance'];
const cardFiles = cardDirs.flatMap(d =>
  fs.readdirSync(path.join(root, d))
    .filter(f => f.endsWith('.md') && f !== 'PENDING.md')
    .map(f => path.join(root, d, f))
);
const protoCache = new Map();
const readProto = name => {
  if (!protoCache.has(name)) {
    const file = path.join(dir, name);
    protoCache.set(name, fs.existsSync(file)
      ? fs.readFileSync(file, 'utf8').replace(/<script[\s\S]*?<\/script>/g, '')
      : null);
  }
  return protoCache.get(name);
};

for (const file of cardFiles) {
  const md = fs.readFileSync(file, 'utf8');
  const rel = path.relative(root, file);
  const problems = [];

  // the card's own prototype must exist
  const pm = md.match(/^\| 原型 \| \[[^\]]*\]\(\.\.\/\.\.\/prototypes\/([^)?]+\.html)\)/m);
  if (!pm) problems.push(`${rel}: missing 原型 row`);
  else if (!readProto(pm[1])) problems.push(`${rel}: missing prototype ${pm[1]}`);

  // every state link must be declared by the target prototype
  for (const m of md.matchAll(/\.\.\/\.\.\/prototypes\/([^)?]+\.html)\?([^)\s]+)/g)) {
    const html = readProto(m[1]);
    if (!html) {
      problems.push(`${rel}: missing prototype ${m[1]}`);
      continue;
    }
    const sv = new URLSearchParams(m[2]).get('state');
    if (!sv) continue;
    const declared = ((html.match(/data-states="([^"]*)"/) || [,''])[1]).split(',').filter(Boolean);
    if (!declared.includes(sv)) problems.push(`${rel}: state=${sv} not declared by ${m[1]} (declared: ${declared.join(',') || 'none'})`);
  }

  // hot spots (scripts stripped) must match the card's interaction rows
  if (pm && readProto(pm[1])) {
    const hots = (readProto(pm[1]).match(/data-hot="/g) || []).length;
    const sec = md.match(/## 交互表\n([\s\S]*?)(?=\n## |$)/);
    const rows = sec ? sec[1].split('\n').filter(l => /^\| [①-⑳] \|/.test(l)).length : 0;
    if (hots !== rows) problems.push(`${rel}: ${hots} hot spot(s) != ${rows} interaction row(s)`);
  }

  if (problems.length) {
    errors += problems.length;
    console.log(`\n[${rel}]`);
    problems.forEach(p => console.log('  - ' + p));
  }
}

// ---- bottom tabbar scope ----
// Business faces never render the base tabbar. The four base tab roots must each
// render exactly one; every other base screen (login, family-create, family-join,
// pin-set, secondary pages) and the non-screen pages render none.
const tabRoots = new Set(['home.html', 'dynamic.html', 'message.html', 'me.html']);
for (const f of files) {
  const html = fs.readFileSync(path.join(dir, f), 'utf8');
  const tabbars = (html.match(/class="tabbar/g) || []).length;
  const face = /^(calendar|todo|reminder|finance)(-|\.html$)/.test(f);
  let problem = null;
  if (face && tabbars) problem = `face page must not render bottom tabbar (found ${tabbars})`;
  else if (tabRoots.has(f) && tabbars !== 1) problem = `base tab root must have exactly 1 tabbar (found ${tabbars})`;
  else if (!face && !tabRoots.has(f) && tabbars) problem = `secondary page must not render bottom tabbar (found ${tabbars})`;
  if (problem) {
    errors += 1;
    console.log(`\n[${f}]`);
    console.log('  - ' + problem);
  }
}

console.log(errors
  ? `\nFAIL: ${errors} problem(s) in ${files.length} pages / ${cardFiles.length} cards`
  : `\nOK: ${files.length} pages / ${cardFiles.length} cards checked`);
process.exit(errors ? 1 : 0);
