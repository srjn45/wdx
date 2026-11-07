// The project pane's rows: sections, agents, worktrees, docs, plans and the
// code tree, filtered and collapsed as the state says, and how each is drawn.
import { P, seg } from './palette.js';
import { DOCS } from './fixture.js';

export function buildRows(s) {
  const rows = [];
  const f = s.filter && s.filter.text ? s.filter.text.toLowerCase() : '';
  const m = (n) => !f || n.toLowerCase().includes(f);
  const head = (sec, label, count) => rows.push({ type: 'head', sec, label, count });
  const top = s.agents.filter((a) => !a.parent && !a.plan);
  head('a', 'Agents', s.agents.length);
  if (!s.collapsed.a) {
    for (const a of top) {
      if (m(a.name)) rows.push({ type: 'agent', a, depth: 0 });
      const kids = s.agents.filter((c) => c.parent === a.id);
      kids.forEach((c, i) => { if (m(c.name)) rows.push({ type: 'agent', a: c, depth: 1, last: i === kids.length - 1 }); });
    }
    for (const p of s.plans.filter((p) => p.state === 'in progress')) {
      const kids = s.agents.filter((a) => a.plan === p.name);
      rows.push({ type: 'plangroup', p, kids });
      if (!s.planFold) kids.forEach((a, i) => { if (m(a.name)) rows.push({ type: 'agent', a, depth: 1, last: i === kids.length - 1 }); });
    }
  }
  head('w', 'Worktrees', s.worktrees.length);
  if (!s.collapsed.w) for (const w of s.worktrees) {
    if (!m(w.name)) continue;
    rows.push({ type: 'wt', w });
    if (w.open) w.files.forEach((fl) => rows.push({ type: 'wtfile', w, f: fl }));
  }
  head('d', 'Docs', s.docs.length + s.docFolders.reduce((n, x) => n + x.docs.length, 0));
  if (!s.collapsed.d) {
    for (const fo of s.docFolders) {
      rows.push({ type: 'docfolder', fo });
      if (fo.open) fo.docs.forEach((d, i) => { if (m(d)) rows.push({ type: 'doc', name: d, depth: 1, last: i === fo.docs.length - 1 }); });
    }
    s.docs.forEach((d) => { if (m(d)) rows.push({ type: 'doc', name: d, depth: 0 }); });
  }
  head('p', 'Plans', s.plans.length);
  if (!s.collapsed.p) {
    rows.push({ type: 'planswitch' });
    s.plans.forEach((p) => { if (m(p.name)) rows.push({ type: 'plan', p }); });
  }
  head('c', 'Code', '');
  if (!s.collapsed.c) {
    rows.push({ type: 'codeswitch' });
    const walk = (nodes, depth, path) => nodes.forEach((n, i) => {
      const p = path ? path + '/' + n.name : n.name;
      if (n.dir) {
        rows.push({ type: 'dir', n, depth, last: i === nodes.length - 1, path: p });
        if (n.open) walk(n.children, depth + 1, p);
      } else if (m(n.name)) rows.push({ type: 'file', n, depth, last: i === nodes.length - 1, path: p });
    });
    walk(s.tree, 0, '');
  }
  return rows;
}
export const selectable = (r) => r.type !== 'head';

export function markSeg(a) {
  if (a.state === 'waiting') return seg('?', P.yellow, true);
  if (a.state === 'exited') return seg('○', P.dim);
  return seg('●', P.green);
}

export function rowParts(s, row, activeKey) {
  const guide = (r) => (r.depth ? '  '.repeat(r.depth - 1) + (r.last ? '└ ' : '├ ') : '');
  switch (row.type) {
    case 'head': return { body: [seg((s.collapsed[row.sec] ? '+' : '−') + ' ' + row.label, P.white, true)], tail: [seg(String(row.count), P.dim)] };
    case 'agent': {
      const a = row.a, act = activeKey === 'agent:' + a.id;
      const tail = [];
      if (a.state === 'working') tail.push(seg('working', P.dim));
      if (a.state === 'waiting') tail.push(seg(a.promptKind || 'yes/no', P.yellow));
      tail.push(seg(a.ctx, parseInt(a.ctx, 10) > 140 ? P.yellow : P.dim));
      return { body: [seg(guide(row), P.dim), markSeg(a), seg(' ' + a.name, act ? P.white : P.fg, act)], tail };
    }
    case 'plangroup': {
      const w = row.kids.filter((k) => k.state === 'waiting').length;
      return { body: [seg(s.planFold ? '+ ' : '− ', P.fg), seg('plan ', P.dim), seg(row.p.name, P.fg)], tail: [seg(row.p.progress, P.dim)].concat(w ? [seg(w + ' waiting', P.yellow)] : []) };
    }
    case 'wt': {
      const w = row.w, t = [];
      if (w.ahead) t.push(seg('↑' + w.ahead + ' +' + w.add + ' −' + w.del, P.dim));
      if (!w.open) t.push(seg((w.ahead ? '· ' : '') + w.files.length + (w.files.length === 1 ? ' file' : ' files'), P.dim));
      return { body: [seg((w.open ? '− ' : '+ ') + w.name, activeKey === 'diff:' + w.name ? P.white : P.fg, activeKey === 'diff:' + w.name)], tail: t };
    }
    case 'wtfile': return { body: [seg('  '), seg(row.f.st, row.f.st === 'A' ? P.green : P.yellow), seg(' ' + row.f.path)], tail: [] };
    case 'docfolder': return { body: [seg((row.fo.open ? '− ' : '+ ') + row.fo.name)], tail: [] };
    case 'doc': {
      const d = DOCS[row.name], act = activeKey === 'doc:' + row.name;
      return { body: [seg(row.depth ? '  └ ' : '  ', P.dim), seg(row.name, act ? P.white : P.fg, act), seg(' ' + d.status, d.status === 'approved' ? P.green : P.dim), seg(d.labels ? ' ' + d.labels : '', P.dim)], tail: [] };
    }
    case 'planswitch': return { body: [seg(['< Draft & in progress >', '< Completed >', '< All >'][s.planView], P.cyan)], tail: [] };
    case 'plan': {
      const act = activeKey === 'plan:' + row.p.name;
      return { body: [seg(row.p.name, act ? P.white : P.fg, act), seg(' ' + row.p.state, row.p.state === 'in progress' ? P.green : P.dim), seg(' ' + row.p.progress, P.dim)], tail: [] };
    }
    case 'codeswitch': return { body: [seg('< ' + ['main', 'checkout-flow', 'fix-auth'][s.codeAt] + ' >', P.cyan)], tail: [] };
    case 'dir': return { body: [seg('  '.repeat(row.depth) + (row.depth ? '' : ''), P.dim), seg((row.n.open ? '− ' : '+ ') + row.n.name)], tail: row.n.count ? [seg(String(row.n.count), P.dim)] : [] };
    case 'file': {
      const act = activeKey === 'file:' + row.path;
      return { body: [seg('  '.repeat(row.depth) + '  ', P.dim), seg(row.n.name, act ? P.white : (row.n.mark ? P.yellow : P.fg), act)], tail: row.n.mark ? [seg(row.n.mark, P.yellow)] : [] };
    }
  }
  return { body: [], tail: [] };
}
