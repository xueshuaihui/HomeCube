import fs from 'node:fs';
import path from 'node:path';

const root = process.cwd();
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

console.log(errors ? `\nFAIL: ${errors} problem(s)` : `\nOK: ${files.length} pages checked`);
process.exit(errors ? 1 : 0);
