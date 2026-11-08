// TideSim: a replica of wdx tide drawn on a canvas, laid out by TIDE's rules.
// Nothing here runs an AI CLI, a daemon or git: everything is scripted.
//
//   new TideSim(host, { rows, maxW, version })  live, in a host element
//   new TideSim(null, { fixed: { W, H, cw, dpr }, version })  offscreen, for images
//   sim.play(TOUR), sim.scene(name), sim.on('caption' | 'drive' | 'size' | 'active', fn)
import { P, seg } from './palette.js';
import { mkState } from './fixture.js';
import { buildRows } from './rows.js';
import { SCENES } from './tour.js';
import { Render } from './render.js';
import { Actions } from './actions.js';
import { Input } from './input.js';

export class TideSim {
  constructor(host, opts) {
    this.opts = opts || {};
    this.host = host;
    this.canvas = document.createElement('canvas');
    this.canvas.setAttribute('role', 'img');
    this.canvas.setAttribute('aria-label', 'An interactive mock-up of wdx tide: a project list on the left, an agent on the right, terminals below.');
    if (host) { host.appendChild(this.canvas); host.tabIndex = 0; }
    this.ctx = this.canvas.getContext('2d');
    this.s = mkState();
    this.hits = []; this.dirty = true; this.active = false; this.visible = true;
    this.spin = 0; this.t = 0; this.playTok = 0; this.playing = false;
    this.listeners = {};
    if (this.opts.fixed) { this.W = this.opts.fixed.W; this.H = this.opts.fixed.H; this.cw = this.opts.fixed.cw; this.ch = Math.round(this.cw * 2.1); this.sizeCanvas(this.opts.fixed.dpr || 1); }
    else this.resize();
    if (host) this.bind();
    this.openTab({ kind: 'agent', ref: 'reviewer' }, false);
    this.render();
  }
  on(ev, fn) { (this.listeners[ev] = this.listeners[ev] || []).push(fn); }
  emit(ev, x) { (this.listeners[ev] || []).forEach((f) => f(x)); }

  /* sizing */
  resize() {
    const cw = Math.max(320, this.host.clientWidth);
    let W = Math.round(cw / 8.2); W = Math.max(84, Math.min(this.opts.maxW || 136, W));
    const cellW = Math.max(7, cw / W);
    this.W = W; this.H = this.opts.rows || 34; this.cw = cellW; this.ch = Math.round(cellW * 2.1);
    this.sizeCanvas(window.devicePixelRatio || 1);
    this.dirty = true;
    this.emit('size', { W: this.W, H: this.H });
    // resizing the canvas clears it: draw again now, not at the next tick
    if (this.grid) this.render();
  }
  sizeCanvas(dpr) {
    this.dpr = dpr;
    const w = this.W * this.cw, h = this.H * this.ch;
    this.canvas.width = Math.round(w * dpr); this.canvas.height = Math.round(h * dpr);
    this.canvas.style.width = w + 'px'; this.canvas.style.height = h + 'px';
  }

  /* layout: the project pane is a quarter of the width, the terminal pane a quarter of the height */
  layout() {
    const s = this.s, W = this.W, avail = this.H - 1;
    const full = { x: 0, y: 0, w: W, h: avail };
    if (s.zoom) return { left: s.zoom === 'left' ? full : null, right: s.zoom === 'right' ? full : null, term: s.zoom === 'term' ? full : null };
    const lw = Math.floor(W / 4) + 1;
    const termH = s.termOpen ? Math.max(3, Math.floor(avail / 4)) : 0;
    const topH = s.termOpen ? avail - termH + 1 : avail;
    return { left: { x: 0, y: 0, w: lw, h: topH }, right: { x: lw - 1, y: 0, w: W - lw + 1, h: topH }, term: s.termOpen ? { x: 0, y: topH - 1, w: W, h: termH } : null };
  }
  inner(r) { return { x: r.x + 1, y: r.y + 1, w: r.w - 2, h: r.h - 2 }; }
  hit(x0, y0, x1, y1, fn, wheel) { this.hits.push({ x0, y0, x1, y1, fn, wheel }); }

  /* ---------- time ---------- */
  tick() {
    const step = () => {
      this.t += 100;
      if (this.t % 200 === 0) { this.spin++; this.dirty = this.dirty || this.anyWorkingShown() || this.cursorShown(); }
      this.advance();
      if (this.dirty && this.visible && !document.hidden) this.render();
      this.timer = setTimeout(step, 100);
    };
    this.timer = setTimeout(step, 100);
  }
  anyWorkingShown() { const t = this.s.tabs[this.s.active]; return !!(t && t.kind === 'agent' && this.agent(t.ref).state === 'working'); }
  cursorShown() { return this.active || this.s.quit; }
  advance() {
    const s = this.s;
    for (const a of s.agents) {
      if (a.state !== 'working') continue;
      if (!a.started) a.started = this.t;
      if (!a.next) a.next = this.t + 1300;
      if (this.t < a.next) continue;
      if (a.stream && a.stream.length) { a.lines.push(a.stream.shift()); a.next = this.t + 1100 + ((a.lines.length * 397) % 900); this.dirty = true; }
      else if (a.loop) { a.next = this.t + 2600; a.lines.push([]); a.stream = a.streamAll.slice(); }
      else if (a.done) { a.state = a.done; a.done = null; this.dirty = true; }
    }
    const sv = s.terms[0];
    if (sv.running === 'server' && this.t % 3300 === 0) {
      const reqs = ['POST /checkout 201 4ms', 'GET /cart/42 200 1ms', 'POST /cart/42/items 201 2ms', 'GET /cart/7 200 1ms', 'POST /checkout 400 bad cart 1ms'];
      sv.lines.push([seg(new Date(Date.UTC(2026, 10, 7, 18, 0, this.t / 1000)).toISOString().slice(11, 19) + ' ', P.dim), seg(reqs[(this.t / 3300) % reqs.length])]);
      if (sv.lines.length > 200) sv.lines.splice(0, 50);
      this.dirty = true;
    }
  }

  /* ---------- autoplay ---------- */
  reset() { this.s = mkState(); this.openTab({ kind: 'agent', ref: 'reviewer' }, false); this.rows = buildRows(this.s); this.dirty = true; }
  stopPlay() { this.playing = false; this.playTok++; this.emit('caption', null); }
  async play(script) {
    const tok = ++this.playTok; this.playing = true;
    const alive = () => this.playing && tok === this.playTok;
    const wait = (ms) => new Promise((r) => setTimeout(r, ms));
    const api = {
      alive, wait, sim: this,
      cap: (t) => this.emit('caption', t),
      walkTo: async (pred) => {
        this.s.focus = 'left'; this.dirty = true;
        for (let n = 0; n < 60 && alive(); n++) {
          const rows = buildRows(this.s); this.rows = rows;
          const target = rows.findIndex(pred); if (target < 0) return;
          if (target === this.s.cursor) return;
          this.moveCursor(target > this.s.cursor ? 1 : -1); await wait(170);
        }
      },
      enter: () => { const r = this.rows[this.s.cursor]; if (r) this.openRow(r); },
      type: async (fn, text) => { for (const ch of text) { if (!alive()) return; fn(ch); this.dirty = true; await wait(70); } },
    };
    while (alive()) {
      this.reset();
      for (const step of script) { if (!alive()) return; await step(api); }
      await wait(1500);
    }
  }
  scene(name) {
    this.stopPlay(); this.reset(); this.rows = buildRows(this.s);
    SCENES[name](this); this.rows = buildRows(this.s); this.dirty = true; this.render();
  }
}

// The methods kept in render.js, actions.js and input.js, by concern.
for (const part of [Render, Actions, Input]) {
  for (const k of Object.getOwnPropertyNames(part.prototype)) {
    if (k !== 'constructor') Object.defineProperty(TideSim.prototype, k, Object.getOwnPropertyDescriptor(part.prototype, k));
  }
}
