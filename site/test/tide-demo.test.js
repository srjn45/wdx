// The tide demo in node, with a canvas that draws nothing: each test reads the
// grid of cells the demo would paint.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { setup, screen, press, click } from './fakedom.js';

setup();
const { TideSim } = await import('../src/tide-demo/sim.js');
const { TOUR, SCENES } = await import('../src/tide-demo/tour.js');

const offscreen = () => new TideSim(null, { fixed: { W: 120, H: 34, cw: 8 }, version: '9.8.7' });

test('panes are laid out as tide lays them out', () => {
  const sim = offscreen();
  const lines = screen(sim);
  assert.equal(lines.length, 34);
  // the project pane is a quarter of the width plus one, sharing its right
  // border with the agent pane; the terminal pane a quarter of the height
  const lw = Math.floor(120 / 4) + 1, termH = Math.floor(33 / 4), topH = 33 - termH + 1;
  assert.match(lines[0], /^┏ \[Project: shop-api\] ━+\[⤢\]━┱─\[● reviewer/);
  assert.equal(lines[0][lw - 1], '┱');
  assert.equal(lines[topH - 1][0], '┡');
  assert.equal(lines[topH - 1][lw - 1], '┹');
  assert.match(lines[33], /C-q menu.*\[wdx 9\.8\.7\]/);
});

test('the left pane lists the project as tide does', () => {
  const text = screen(offscreen()).join('\n');
  for (const row of ['− Agents', '● reviewer', '? fix-auth', '+ checkout-flow', '− Worktrees']) assert.ok(text.includes(row), row);
});

test('every scene renders', () => {
  for (const name of Object.keys(SCENES)) {
    const sim = offscreen();
    sim.scene(name);
    assert.equal(screen(sim).length, 34, name);
  }
});

test('the tour plays to its end and starts again', async () => {
  const { sim } = live();
  const captions = [];
  sim.on('caption', (t) => t && captions.push(t));
  // the tour's waits, a hundred times shorter
  const real = globalThis.setTimeout;
  globalThis.setTimeout = (fn, ms) => real(fn, (ms || 0) / 100);
  try {
    const done = sim.play(TOUR);
    while (captions.length <= TOUR.length) await new Promise((r) => real(r, 5));
    sim.stopPlay();
    await done;
  } finally {
    globalThis.setTimeout = real;
  }
  assert.equal(captions[TOUR.length], captions[0]);
});

function live() {
  const host = document.createElement('div');
  const sim = new TideSim(host, { rows: 34, version: '9.8.7' });
  clearTimeout(sim.timer);
  return { sim, host };
}

test('keys open rows, the menu, zoom and the terminal', () => {
  const { sim, host } = live();
  host.focus();
  for (const k of ['ArrowDown', 'ArrowDown', 'Enter']) press(host, k);
  press(host, ' ', { ctrl: true });
  assert.ok(sim.s.popup, 'Ctrl+Space opens the menu');
  press(host, 'Escape');
  assert.equal(sim.s.popup, null);
  press(host, ']', { ctrl: true });
  assert.ok(sim.s.zoom, 'C-] zooms');
  press(host, ']', { ctrl: true });
  assert.equal(sim.s.zoom, null);
  press(host, '\\', { ctrl: true });
  assert.equal(sim.s.termOpen, false, 'C-\\ hides the terminal');
});

test('a new agent is created from the menu', () => {
  const { sim, host } = live();
  host.focus();
  const before = sim.s.agents.length;
  press(host, 'a');
  press(host, '2');
  assert.equal(sim.s.agents.length, before + 1);
});

test('the shell runs wdx agent list', () => {
  const { sim, host } = live();
  host.focus();
  sim.s.focus = 'term'; sim.s.termActive = 1;
  const tm = sim.s.terms[1], before = tm.lines.length;
  for (const c of 'wdx agent list') press(host, c);
  press(host, 'Enter');
  sim.render();
  assert.ok(tm.lines.length > before + 3);
  assert.ok(tm.lines.flat().some((seg) => seg.t && seg.t.includes('fix-auth')));
});

test('answering the waiting agent sets it working', () => {
  const sim = offscreen();
  sim.scene('answer');
  const a = sim.agent('fix-auth');
  assert.equal(a.state, 'waiting');
  sim.answer(a, 0);
  assert.equal(a.state, 'working');
});

test('every click target and the wheel work without an error', () => {
  const { sim } = live();
  sim.render();
  let clicked = 0;
  for (const h of sim.hits.slice()) {
    if (!h.fn) continue;
    sim.render();
    click(sim, h.x0, h.y0);
    clicked++;
  }
  assert.ok(clicked > 20, `${clicked} targets`);
  sim.canvas.listeners.wheel({ clientX: 100, clientY: 100, deltaY: 100, preventDefault() {} });
  sim.render();
});

test('quitting tide and starting it again keeps the agents', () => {
  const { sim, host } = live();
  host.focus();
  const n = sim.s.agents.length;
  sim.quit(); sim.render();
  assert.match(screen(sim).join('\n'), /tide closed/);
  press(host, 'Enter'); sim.render();
  assert.equal(sim.s.quit, false);
  assert.equal(sim.s.agents.length, n);
});
