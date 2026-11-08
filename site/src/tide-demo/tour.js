// The hero's tour, step by step with its captions, and the named scenes the
// screens on the page are drawn from.
import { buildRows } from './rows.js';

/* the hero's tour */
export const TOUR = [
  async (x) => { x.cap('TIDE shows a project: its agents, worktrees, docs, plans and code on the left, an agent on the right, terminals below.'); await x.wait(3600); },
  async (x) => { x.cap('reviewer is working. TIDE reads each agent\'s screen to tell working, waiting and idle apart.'); await x.walkTo((r) => r.type === 'agent' && r.a.id === 'reviewer'); x.enter(); await x.wait(3800); },
  async (x) => { x.cap('fix-auth needs you: its row turns amber, with the kind of question it asks.'); await x.walkTo((r) => r.type === 'agent' && r.a.id === 'fix-auth'); await x.wait(1400); x.enter(); x.sim.s.focus = 'right'; await x.wait(2200); },
  async (x) => { x.cap('Answer it without leaving TIDE; it goes back to work.'); const a = x.sim.agent('fix-auth'); if (!a.prompt) return; a.prompt.sel = 1; x.sim.dirty = true; await x.wait(700); a.prompt.sel = 0; x.sim.dirty = true; await x.wait(700); x.sim.answer(a, 0); await x.wait(3400); },
  async (x) => { x.cap('Each agent works in a git worktree of its own. Read its diff, then land it.'); await x.walkTo((r) => r.type === 'wt' && r.w.name === 'checkout-flow'); x.enter(); await x.wait(1600); x.sim.s.focus = 'right'; for (let i = 0; i < 8 && x.alive(); i++) { x.sim.s.tabs[x.sim.s.active].scroll++; x.sim.dirty = true; await x.wait(160); } await x.wait(1800); },
  async (x) => { x.cap('A plan splits a feature into tasks. wdx runs an agent for each task that is ready and lands what it finishes.'); await x.walkTo((r) => r.type === 'plan' && r.p.name === 'checkout'); x.enter(); x.sim.s.focus = 'right'; await x.wait(1800); const p = x.sim.s.plans[0]; for (const i of [2, 3, 0, 1]) { p.sel = i; x.sim.dirty = true; await x.wait(900); } },
  async (x) => { x.cap('Browse the code of any worktree; changed files are marked.'); await x.walkTo((r) => r.type === 'dir' && r.n.name === 'internal'); x.sim.expand(true); await x.wait(500); await x.walkTo((r) => r.type === 'dir' && r.n.name === 'checkout'); x.sim.expand(true); await x.wait(400); await x.walkTo((r) => r.type === 'file' && r.n.name === 'checkout.go'); x.enter(); await x.wait(2600); },
  async (x) => { x.cap('Terminals sit below. Like agents, they keep running when TIDE closes.'); const s = x.sim.s; s.focus = 'term'; s.termActive = 1; x.sim.dirty = true; await x.wait(600); const tm = s.terms[1]; await x.type((c) => { tm.input += c; }, 'wdx agent list'); await x.wait(400); tm.input = ''; x.sim.runShell(tm, 'wdx agent list'); x.sim.dirty = true; await x.wait(3200); },
  async (x) => { x.cap('C-q shows what each key does, wherever you are.'); x.sim.s.focus = 'left'; x.sim.openMenu(); await x.wait(3200); x.sim.s.popup = null; x.sim.dirty = true; },
  async (x) => { x.cap('C-] zooms the pane you are in to the full window.'); x.sim.s.focus = 'right'; x.sim.toggleZoom('right'); await x.wait(2600); x.sim.toggleZoom(); await x.wait(800); },
];

/* scenes for the gallery and the guided section */
export const SCENES = {
  agents(sim) { const s = sim.s; s.cursor = buildRows(s).findIndex((r) => r.type === 'agent' && r.a.id === 'fix-auth'); const a = sim.agent('reviewer'); a.lines = a.lines.concat(a.stream.splice(0, 6)); sim.openTab({ kind: 'agent', ref: 'reviewer' }); s.focus = 'left'; },
  answer(sim) { const s = sim.s; sim.openTab({ kind: 'agent', ref: 'fix-auth' }); s.cursor = buildRows(s).findIndex((r) => r.type === 'agent' && r.a.id === 'fix-auth'); s.focus = 'right'; },
  diff(sim) { const s = sim.s; s.worktrees[1].open = true; sim.openTab({ kind: 'diff', ref: 'checkout-flow' }); s.cursor = buildRows(s).findIndex((r) => r.type === 'wt' && r.w.name === 'checkout-flow'); s.focus = 'right'; s.tabs[s.active].scroll = 3; },
  plan(sim) { const s = sim.s; sim.openTab({ kind: 'plan', ref: 'checkout' }); s.cursor = buildRows(s).findIndex((r) => r.type === 'plan'); s.focus = 'right'; },
  code(sim) { const s = sim.s; s.collapsed.d = true; s.collapsed.w = true; s.tree[2].open = true; s.tree[2].children[2].open = true; sim.openTab({ kind: 'file', ref: 'internal/checkout/checkout.go' }); s.cursor = buildRows(s).findIndex((r) => r.type === 'file' && r.n.name === 'checkout.go'); s.focus = 'left'; },
  menu(sim) { const s = sim.s; s.cursor = buildRows(s).findIndex((r) => r.type === 'wt' && r.w.name === 'checkout-flow'); sim.rows = buildRows(s); sim.openTab({ kind: 'diff', ref: 'checkout-flow' }); s.focus = 'left'; sim.openMenu(); },
  terminal(sim) { const s = sim.s; s.focus = 'term'; s.termActive = 1; sim.runShell(s.terms[1], 'wdx agent list'); s.zoom = null; },
  zoom(sim) { const s = sim.s; const a = sim.agent('reviewer'); a.lines = a.lines.concat(a.stream.splice(0, 12)); sim.openTab({ kind: 'agent', ref: 'reviewer' }); s.focus = 'right'; s.zoom = 'right'; },
};
