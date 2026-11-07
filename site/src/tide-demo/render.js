// TideSim's drawing: each pane, tab, popup and the status line onto a Grid,
// then the Grid onto the canvas. Mixed into TideSim by sim.js.
import { P, seg, len, segsLen } from './palette.js';
import { BOX, Grid } from './grid.js';
import { FILES, DIFFS, DOCS, ACA_NAMES, prompt } from './fixture.js';
import { buildRows, markSeg, rowParts } from './rows.js';
import { hl, diffLines } from './highlight.js';

export class Render {
  /* ---------- render ---------- */
  render() {
    const s = this.s, g = new Grid(this.W, this.H);
    this.hits = [];
    const L = this.layout();
    this.lay = L;
    const panes = [['left', L.left], ['right', L.right], ['term', L.term]];
    for (const [k, r] of panes) if (r) g.fill(r.x + 1, r.y + 1, r.w - 2, r.h - 2, s.focus === k ? P.fbg : P.bg);
    for (const [k, r] of panes) if (r && s.focus !== k) g.box(r.x, r.y, r.w, r.h, 1, P.line);
    for (const [k, r] of panes) if (r && s.focus === k) g.box(r.x, r.y, r.w, r.h, 2, P.green);
    if (L.left) this.drawLeft(g, L.left);
    if (L.right) this.drawRight(g, L.right);
    if (L.term) this.drawTerm(g, L.term);
    for (const [k, r] of panes) if (r) {
      const zx = r.x + r.w - 5;
      g.text(zx, r.y, '[⤢]', P.dim, s.focus === k ? null : null);
      this.hit(zx, r.y, zx + 2, r.y, () => this.toggleZoom(k));
      this.hit(r.x, r.y, r.x + r.w - 1, r.y + r.h - 1, null, k);
    }
    this.drawStatus(g);
    if (s.popup) this.drawPopup(g);
    if (s.quit) this.drawQuit(g);
    this.grid = g;
    this.paint(g);
    this.dirty = false;
  }

  drawLeft(g, R) {
    const s = this.s, I = this.inner(R);
    g.text(R.x + 1, R.y, ' [Project: shop-api] ', P.white, null, true);
    const rows = buildRows(s); this.rows = rows;
    if (s.cursor >= rows.length) s.cursor = rows.length - 1;
    const listH = I.h - (s.filter ? 1 : 0);
    if (s.cursor < s.lscroll) s.lscroll = s.cursor;
    if (s.cursor >= s.lscroll + listH) s.lscroll = s.cursor - listH + 1;
    s.lscroll = Math.max(0, Math.min(s.lscroll, Math.max(0, rows.length - listH)));
    const at = this.activeKey();
    for (let i = 0; i < listH; i++) {
      const idx = s.lscroll + i, row = rows[idx]; if (!row) break;
      const y = I.y + i, cur = idx === s.cursor;
      const bg = row.type === 'head' ? P.head : cur ? (s.focus === 'left' ? P.sel : P.tint) : (s.focus === 'left' ? P.fbg : P.bg);
      g.fill(I.x, y, I.w, 1, bg);
      const parts = rowParts(s, row, at);
      if (this.rowIsActive(row, at)) g.text(I.x, y, '▌', P.green, bg);
      if (cur) g.text(I.x + 1, y, '▸', P.white, bg, true);
      const tailStr = parts.tail.filter((t) => t && t.t);
      const bx = row.type === 'head' ? I.x + 1 : I.x + 2;
      const tws = () => (tailStr.length ? tailStr.reduce((n, t) => n + len(t.t), 0) + tailStr.length - 1 : 0);
      const want = Math.min(segsLen(parts.body), 14);
      while (tailStr.length && (I.x + I.w - 2 - tws()) - bx < want) tailStr.pop();
      const tw = tws();
      const tx = I.x + I.w - 1 - tw;
      const maxB = tw ? tx - 1 : I.x + I.w - 1;
      const end = g.segs(bx, y, parts.body, maxB, bg);
      if (segsLen(parts.body) > maxB - bx) g.text(maxB - 1, y, '…', P.dim, bg);
      let x = tx;
      tailStr.forEach((t, j) => { x = g.text(x, y, t.t, t.fg, bg, t.b); if (j < tailStr.length - 1) x = g.text(x, y, ' ', null, bg); });
      void end;
      this.hit(I.x, y, I.x + I.w - 1, y, (e) => this.clickRow(idx, e));
    }
    if (s.filter) {
      const y = I.y + I.h - 1; g.fill(I.x, y, I.w, 1, P.head);
      g.segs(I.x + 1, y, [seg('/ ', P.cyan, true), seg(s.filter.text), seg(s.focus === 'left' && this.cursorOn() ? '█' : ' ', P.white)], I.x + I.w, P.head);
    }
  }
  rowIsActive(row, at) {
    if (!at) return false;
    switch (row.type) {
      case 'agent': return at === 'agent:' + row.a.id;
      case 'wt': return at === 'diff:' + row.w.name;
      case 'doc': return at === 'doc:' + row.name;
      case 'plan': return at === 'plan:' + row.p.name;
      case 'file': return at === 'file:' + row.path;
    }
    return false;
  }
  cursorOn() { return Math.floor(this.t / 530) % 2 === 0; }
  activeKey() { const t = this.s.tabs[this.s.active]; return t ? t.kind + ':' + t.ref : null; }

  drawRight(g, R) {
    const s = this.s, I = this.inner(R);
    let x = R.x + 2;
    const maxX = R.x + R.w - 6;
    s.tabs.forEach((t, i) => {
      const act = i === s.active, fg = act ? P.white : P.dim;
      const title = this.tabTitle(t);
      const w = segsLen(title) + 5;
      if (x + w > maxX) return;
      const x0 = x;
      x = g.text(x, R.y, '[', fg, null, act);
      for (const sg of title) x = g.text(x, R.y, sg.t, sg.fg && sg.fg !== P.fg ? sg.fg : fg, null, act);
      const cx = x; x = g.text(x, R.y, '(-)', P.dim); x = g.text(x, R.y, ']', fg, null, act);
      this.hit(x0, R.y, cx - 1, R.y, () => { this.s.active = i; this.s.focus = 'right'; this.dirty = true; });
      this.hit(cx, R.y, cx + 2, R.y, () => this.closeTab(i));
      x += 1;
    });
    const t = s.tabs[s.active];
    if (!t) { g.segs(I.x, I.y, [seg('no tab open: Enter on a row of the project opens one', P.dim)], I.x + I.w); return; }
    if (t.kind === 'agent') return this.drawAgent(g, I, t);
    if (t.kind === 'plan') return this.drawPlan(g, I, t);
    let lines = [], footer = null, lineNo = false;
    if (t.kind === 'diff') {
      lines = diffLines(DIFFS[t.ref]);
      const chunks = lines.filter((l) => l[0].fg === P.cyan).length;
      footer = { left: [seg(' [ ] ', P.cyan), seg('change  ', P.dim), seg('{ } ', P.cyan), seg('file  ', P.dim), seg('L ', P.cyan), seg('land  ', P.dim), seg('C ', P.cyan), seg('commit  ', P.dim), seg('? ', P.cyan), seg('keys', P.dim)], right: [seg('change 1 of ' + chunks + ' · file 1 of 2', P.dim)] };
    } else if (t.kind === 'file') {
      const lang = t.ref.endsWith('.md') ? 'md' : 'go';
      lines = (FILES[t.ref] || '').split('\n').map((l) => hl(l, lang)); lineNo = true;
      footer = { left: [seg(' e ', P.cyan), seg('edit  ', P.dim), seg('y ', P.cyan), seg('copy path  ', P.dim), seg('- ', P.cyan), seg('close tab', P.dim)], right: [seg(t.ref + ' · read-only', P.dim)] };
    } else if (t.kind === 'doc') {
      const d = DOCS[t.ref];
      lines = [[seg(' ' + t.ref, P.white, true), seg('  ' + d.status, d.status === 'approved' ? P.green : P.yellow), seg('  rev ' + d.rev + (d.labels ? '  · ' + d.labels : ''), P.dim)], []].concat(d.body.split('\n').map((l) => [seg(' ')].concat(hl(l, 'md'))));
      footer = { left: [seg(' e ', P.cyan), seg('edit  ', P.dim), seg('h ', P.cyan), seg('history  ', P.dim), seg('- ', P.cyan), seg('close tab', P.dim)], right: [seg('wdx doc get --name ' + t.ref, P.dim)] };
    } else if (t.kind === 'settings') {
      const rows = [['tide.theme', 'dark', 'tide\'s colors: dark, light or none'], ['tide.section_order', 'awdpc', 'the sections of the project pane, in order'], ['agent.default_aca', 'claude_code', 'the AI coding agent new agents run'], ['aca.codex.enabled', 'true', 'whether new agents may run codex'], ['aca.cursor.enabled', 'true', 'whether new agents may run cursor'], ['update.channel', 'stable', 'the releases wdx update looks at'], ['log.level', 'info', 'how much the logs say']];
      lines = [[seg(' Settings', P.white, true), seg('   from ~/.wdx/config.yaml', P.dim)], []].concat(rows.map(([k, v, d]) => [seg(' ' + k.padEnd(22), P.cyan), seg(v.padEnd(14), P.white, true), seg(d, P.dim)]));
      footer = { left: [seg(' Enter ', P.cyan), seg('change  ', P.dim), seg('r ', P.cyan), seg('reset', P.dim)], right: [] };
    } else if (t.kind === 'info') {
      const a = this.agent(t.ref);
      const kv = [['name', a.name], ['ai cli', ACA_NAMES[a.aca]], ['model', a.model], ['state', a.state === 'waiting' ? 'running · waiting for you' : a.state === 'working' ? 'running · working' : 'running · idle'], ['worktree', a.wt || 'main'], ['context', a.ctx + ' tokens'], ['parent', a.parent || (a.plan ? 'plan ' + a.plan : '—')], ['session', 'resumes on wdx agent restart']];
      lines = [[seg(' ' + a.name, P.white, true), seg('  info', P.dim)], []].concat(kv.map(([k, v]) => [seg(' ' + k.padEnd(10), P.dim), seg(v)]));
    }
    const bodyH = I.h - (footer ? 1 : 0);
    t.scroll = Math.max(0, Math.min(t.scroll || 0, Math.max(0, lines.length - bodyH)));
    const gut = lineNo ? 4 : 0;
    for (let i = 0; i < bodyH; i++) {
      const li = t.scroll + i, l = lines[li]; if (!l) break;
      const y = I.y + i;
      const lineBg = l[0] && l[0].bg;
      if (lineBg) g.fill(I.x + gut, y, I.w - gut, 1, lineBg);
      if (lineNo) g.text(I.x, y, String(li + 1).padStart(3) + ' ', li === (t.line || 0) ? P.white : P.dim, null, li === (t.line || 0));
      g.segs(I.x + gut + 1, y, l, I.x + I.w);
    }
    if (footer) {
      const y = I.y + I.h - 1;
      g.segs(I.x, y, footer.left, I.x + I.w);
      const rw = segsLen(footer.right); g.segs(I.x + I.w - rw - 1, y, footer.right, I.x + I.w);
    }
  }

  tabTitle(t) {
    switch (t.kind) {
      case 'agent': { const a = this.agent(t.ref); return [markSeg(a), seg(' ' + a.name)]; }
      case 'diff': return [seg((t.ref === 'main' ? 'uncommitted: ' : 'diff: ') + t.ref)];
      case 'plan': return [seg('plan: ' + t.ref + ' r1')];
      case 'file': return [seg('file: ' + t.ref.split('/').pop())];
      case 'doc': return [seg('doc: ' + t.ref)];
      case 'settings': return [seg('settings')];
      case 'info': return [seg('info: ' + t.ref)];
    }
    return [seg('?')];
  }

  drawAgent(g, I, t) {
    const a = this.agent(t.ref), s = this.s;
    const foc = s.focus === 'right';
    let bottomH;
    if (a.state === 'waiting' && a.prompt) bottomH = a.prompt.opts.length + 5; else bottomH = 4;
    const lines = a.lines.slice();
    if (a.state === 'working') {
      const fr = '·✢✳✶✻✽✻✶✳✢'[this.spin % 10];
      const secs = Math.max(1, Math.floor((this.t - (a.started || 0)) / 1000));
      lines.push([seg(fr + ' ', P.orange, true), seg('Working… ', P.orange), seg('(' + secs + 's · esc to interrupt)', P.dim)]);
    }
    const bodyH = I.h - bottomH;
    t.scroll = Math.max(0, Math.min(t.scroll || 0, Math.max(0, lines.length - bodyH)));
    const start = Math.max(0, lines.length - bodyH - t.scroll);
    for (let i = 0; i < bodyH; i++) {
      const l = lines[start + i]; if (!l) break;
      g.segs(I.x + 1, I.y + i, l, I.x + I.w - 1);
    }
    const by = I.y + I.h - bottomH;
    const bw = I.w - 2;
    if (a.state === 'waiting' && a.prompt) {
      const pr = a.prompt, h = pr.opts.length + 4;
      g.box(I.x + 1, by, bw, h, 1, P.yellow);
      g.text(I.x + 3, by, ' ' + pr.q + ' ', P.yellow, null, true);
      g.text(I.x + 3, by + 1, pr.cmd, P.white, null, true);
      pr.opts.forEach((o, i) => {
        const y = by + 3 + i, on = i === pr.sel;
        g.segs(I.x + 3, y, [seg(on ? '› ' : '  ', P.cyan, true), seg((i + 1) + '. ' + o, on ? P.cyan : P.fg, on)], I.x + bw);
        this.hit(I.x + 2, y, I.x + bw - 1, y, () => { pr.sel = i; this.answer(a, i); });
      });
      g.text(I.x + 2, by + h, 'Enter answer · 1-3 choose · tide marks this agent ? until you answer', P.dim);
    } else {
      g.box(I.x + 1, by, bw, 3, 1, foc ? P.dim : P.line);
      g.segs(I.x + 3, by + 1, [seg('> ', P.dim), seg(a.input || ''), seg(foc && this.cursorOn() ? '█' : ' ', P.white)], I.x + bw - 1);
      if (!a.input && !foc) g.text(I.x + 5, by + 1, 'click here and type to talk to ' + a.name, P.dim);
      g.text(I.x + 2, by + 3, '? for shortcuts', P.dim);
      const k = ACA_NAMES[a.aca] + ' · ' + a.ctx + ' context';
      g.text(I.x + I.w - len(k) - 2, by + 3, k, P.dim);
      this.hit(I.x + 1, by, I.x + bw, by + 2, () => { s.focus = 'right'; this.dirty = true; });
    }
  }

  drawPlan(g, I, t) {
    const p = this.s.plans.find((x) => x.name === t.ref);
    const done = p.tasks.filter((k) => k.state === 'done').length, working = p.tasks.filter((k) => k.state === 'working').length;
    g.segs(I.x + 1, I.y, [seg(p.name, P.white, true)], I.x + I.w);
    const r = [seg('● ', P.green), seg(p.state + '   ', P.fg), seg('rev 1', P.dim)];
    g.segs(I.x + I.w - segsLen(r) - 1, I.y, r, I.x + I.w);
    g.text(I.x + 1, I.y + 1, p.tasks.length + ' tasks · ' + done + ' done · ' + working + ' working · ' + (p.tasks.length - done - working) + ' waiting', P.dim);
    g.segs(I.x + 1, I.y + 2, [seg('Goal  ', P.dim), seg('Let a customer place an order from their cart.')], I.x + I.w);
    const by = I.y + 3, bh = I.h - 4, lw = Math.min(36, Math.floor(I.w * 0.38));
    g.box(I.x, by, lw + 1, bh, 1, P.line); g.box(I.x + lw, by, I.w - lw, bh, 1, P.line);
    g.text(I.x + 2, by, ' Tasks ', P.fg);
    const sel = p.tasks[p.sel];
    g.text(I.x + lw + 2, by, ' ' + sel.id + ' ', P.white, null, true);
    const rail = [['✓', '    '], ['├─╮', null], ['●', ' │  '], ['│ ●', '  '], ['├─╯', null], ['○', '    ']];
    const glyph = { done: ['✓', P.green], working: ['●', P.green], waiting: ['○', P.dim] };
    const tx = [0, null, 1, 2, null, 3];
    rail.forEach(([rl], i) => {
      const y = by + 1 + i, ti = tx[i];
      if (ti === null) { g.text(I.x + 2, y, rl, P.dim); return; }
      const k = p.tasks[ti], on = ti === p.sel, bg = on ? (this.s.focus === 'right' ? P.sel : P.tint) : null;
      if (bg) g.fill(I.x + 1, y, lw - 1, 1, bg);
      if (on) g.text(I.x + 1, y, '▌', P.green, bg);
      const [gl, gc] = glyph[k.state];
      if (i === 2) { g.text(I.x + 2, y, gl, gc, bg, true); g.text(I.x + 4, y, '│', P.dim, bg); }
      else if (i === 3) { g.text(I.x + 2, y, '│', P.dim, bg); g.text(I.x + 4, y, gl, gc, bg, true); }
      else g.text(I.x + 2, y, gl, gc, bg, true);
      g.segs(I.x + 8, y, [seg(k.id.padEnd(8), on ? P.white : P.fg, on), seg(k.state, k.state === 'working' ? P.green : P.dim)], I.x + lw, bg);
      this.hit(I.x + 1, y, I.x + lw - 1, y, () => { p.sel = ti; this.s.focus = 'right'; this.dirty = true; });
    });
    const dx = I.x + lw + 2; let y = by + 1;
    const line = (ss) => { g.segs(dx, y++, ss, I.x + I.w - 1); };
    const [gl, gc] = glyph[sel.state];
    line([seg(gl + ' ', gc, true), seg(sel.state), seg(sel.agent ? '  ·  agent ' + sel.agent : '', P.dim)]); y++;
    if (sel.after.length) { line([seg('Waits for', P.dim)]); sel.after.forEach((a) => line([seg('  ✓ ', P.green), seg(a)])); y++; }
    const unb = p.tasks.filter((k) => k.after.includes(sel.id));
    if (unb.length) { line([seg('Unblocks', P.dim)]); unb.forEach((k) => line([seg('  ○ ', P.dim), seg(k.id)])); y++; }
    line([seg('Prompt', P.dim)]); line([seg('  ' + sel.prompt)]); y++;
    if (sel.agent) line([seg('Enter ', P.cyan), seg('open its agent', P.dim)]);
    g.segs(I.x + 1, I.y + I.h - 1, [seg('p ', P.cyan), seg('pause  ', P.dim), seg('y ', P.cyan), seg('copy  ', P.dim), seg('↑↓ ', P.cyan), seg('select  ', P.dim), seg('Enter ', P.cyan), seg('open', P.dim)], I.x + I.w);
  }

  drawTerm(g, R) {
    const s = this.s, I = this.inner(R);
    let x = R.x + 1; g.text(x++, R.y, ' ');
    s.terms.forEach((tm, i) => {
      const act = i === s.termActive, x0 = x;
      x = g.text(x, R.y, '[', act ? P.white : P.dim, null, act);
      x = g.text(x, R.y, tm.state === 'running' ? '●' : '○', tm.state === 'running' ? P.green : P.dim, null, act);
      x = g.text(x, R.y, ' ' + tm.name + ']', act ? P.white : P.dim, null, act);
      this.hit(x0, R.y, x - 1, R.y, () => { s.termActive = i; s.focus = 'term'; this.dirty = true; });
      x = g.text(x, R.y, ' ');
    });
    const tm = s.terms[s.termActive];
    const lines = tm.lines.slice();
    if (!tm.running) lines.push(prompt().concat([seg(tm.input), seg(s.focus === 'term' && this.cursorOn() ? '█' : ' ', P.white)]));
    tm.scroll = Math.max(0, Math.min(tm.scroll || 0, Math.max(0, lines.length - I.h)));
    const start = Math.max(0, lines.length - I.h - tm.scroll);
    for (let i = 0; i < I.h; i++) { const l = lines[start + i]; if (!l) break; g.segs(I.x, I.y + i, l, I.x + I.w); }
    if (!tm.lines.length && s.focus !== 'term') g.text(I.x + 22, I.y, 'click here and try: wdx agent list', P.dim);
  }

  drawStatus(g) {
    const s = this.s, y = this.H - 1;
    const where = { left: 'project', right: 'agent', term: 'terminal' }[s.focus] + (s.zoom ? ' · zoomed' : '');
    g.segs(0, y, [seg(' shop-api', P.white, true), seg(' · ' + where, P.dim)], this.W);
    const ver = seg('[wdx ' + (this.opts.version || '0.0.0') + ']', P.white, true);
    let right;
    if (s.notice && this.t < s.noticeUntil) right = [seg(s.notice, P.yellow), seg('  '), ver];
    else right = [seg('Alt+arrows ', P.cyan), seg('move  ', P.dim), seg('C-] ', P.cyan), seg('zoom  ', P.dim), seg('C-\\ ', P.cyan), seg('terminal  ', P.dim), seg('C-q ', P.cyan), seg('menu  ', P.dim), ver];
    const rw = segsLen(right), x = Math.max(20, this.W - rw - 1);
    g.segs(x, y, right, this.W);
    let off = x;
    for (const sg of right) { if (sg.t === 'C-q ') { this.hit(off, y, off + 8, y, () => this.openMenu()); break; } off += len(sg.t); }
  }

  drawPopup(g) {
    const pu = this.s.popup;
    const lines = [];
    pu.sections.forEach((sec, si) => {
      if (si) lines.push({ blank: true });
      if (sec.title) lines.push({ title: sec.title });
      sec.items.forEach((it) => lines.push({ it }));
    });
    const foot = pu.foot || '↑↓ select  Enter choose  Esc close';
    const iw = Math.max(26, len(pu.title) + 4, len(foot) + 4, ...lines.map((l) => (l.it ? len(l.it.label) + 7 : l.title ? len(l.title) + 4 : 0)));
    const ih = lines.length + 6, ow = iw + 6, oh = ih + 4;
    const ox = Math.floor((this.W - ow) / 2), oy = Math.max(0, Math.floor((this.H - 1 - oh) / 2));
    g.fill(ox, oy, ow, oh, P.bg);
    for (let j = oy; j < oy + oh; j++) for (let i = ox; i < ox + ow; i++) { const c = g.cell(i, j); if (c) { c.ch = ' '; c.u = c.d = c.l = c.r = 0; c.lw = 0; } }
    g.box(ox, oy, ow, oh, 2, P.green);
    const x = ox + 3, y = oy + 2;
    g.box(x, y, iw, ih, 2, P.green); g.hline(x, x + iw - 1, y + 2, 2, P.green); g.hline(x, x + iw - 1, y + ih - 3, 2, P.green);
    g.text(x + 2, y + 1, pu.title, P.white, null, true);
    let k = 0;
    lines.forEach((l, i) => {
      const yy = y + 3 + i;
      if (l.title) g.text(x + 2, yy, l.title, P.dim);
      if (l.it) {
        const on = k === pu.sel, myK = k;
        if (on) g.fill(x + 1, yy, iw - 2, 1, P.sel);
        g.segs(x + 2, yy, [seg((l.it.k || ' ').padEnd(3), P.cyan, true), seg(l.it.label, on ? P.white : P.fg, on)], x + iw - 1, on ? P.sel : null);
        this.hit(x + 1, yy, x + iw - 2, yy, () => { pu.sel = myK; this.runPopup(); });
        k++;
      }
    });
    g.text(x + 2, y + ih - 2, foot, P.dim);
    this.hits.push({ x0: 0, y0: 0, x1: this.W, y1: this.H, fn: null, modal: [ox, oy, ox + ow - 1, oy + oh - 1] });
  }

  drawQuit(g) {
    for (const c of g.cells) { c.ch = ' '; c.u = c.d = c.l = c.r = 0; c.lw = 0; c.bg = '#16191f'; }
    const msg = [[seg('$ wdx tide', P.dim)], [seg('tide closed', P.white, true)], [], [seg('Every agent and terminal is still running in the daemon.', P.fg)], [seg('Start tide again and it comes back as you left it.', P.fg)], [], [seg('$ ', P.green), seg('wdx tide', P.white, true), seg(this.cursorOn() ? '█' : ' ', P.white)], [], [seg('Enter or click to start it again', P.dim)]];
    const y0 = Math.floor(this.H / 2) - 5;
    msg.forEach((l, i) => g.segs(4, y0 + i, l, this.W, '#16191f'));
    this.hits = [{ x0: 0, y0: 0, x1: this.W, y1: this.H, fn: () => this.unquit() }];
  }

  paint(g) {
    const ctx = this.ctx, cw = this.cw, ch = this.ch, dpr = this.dpr;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    const fs = cw / 0.6;
    const fam = '"JetBrains Mono", ui-monospace, Menlo, monospace';
    ctx.textBaseline = 'middle';
    const thin = Math.max(1, Math.round(cw * 0.13 * dpr) / dpr), thick = Math.max(2, Math.round(cw * 0.3 * dpr) / dpr);
    let font = '';
    for (let y = 0; y < g.h; y++) {
      for (let x = 0; x < g.w; x++) {
        const c = g.cells[y * g.w + x], px = x * cw, py = y * ch;
        ctx.fillStyle = c.bg; ctx.fillRect(Math.floor(px), Math.floor(py), Math.ceil(cw) + 1, Math.ceil(ch) + 1);
        let arms = c.lw ? [c.u, c.d, c.l, c.r] : null, lc = c.lc;
        if (!arms && BOX[c.ch]) { arms = BOX[c.ch]; lc = c.fg; }
        if (arms) {
          ctx.fillStyle = lc;
          const cx = px + cw / 2, cy = py + ch / 2;
          const [u, d, l, r] = arms;
          const tw = (w) => (w === 2 ? thick : thin);
          const vt = Math.max(u, d) === 2 ? thick : thin, ht = Math.max(l, r) === 2 ? thick : thin;
          if (u) ctx.fillRect(cx - tw(u) / 2, py, tw(u), ch / 2 + ht / 2);
          if (d) ctx.fillRect(cx - tw(d) / 2, cy - ht / 2, tw(d), ch / 2 + ht / 2 + 0.5);
          if (l) ctx.fillRect(px, cy - tw(l) / 2, cw / 2 + vt / 2, tw(l));
          if (r) ctx.fillRect(cx - vt / 2, cy - tw(r) / 2, cw / 2 + vt / 2 + 0.5, tw(r));
          continue;
        }
        if (c.ch === ' ') continue;
        if (c.ch === '▌') { ctx.fillStyle = c.fg; ctx.fillRect(px, py, cw / 2, ch); continue; }
        if (c.ch === '█') { ctx.fillStyle = c.fg; ctx.fillRect(px, py + 1, cw, ch - 2); continue; }
        const f = (c.b ? '700 ' : '400 ') + fs.toFixed(2) + 'px ' + fam;
        if (f !== font) { ctx.font = f; font = f; }
        ctx.fillStyle = c.fg;
        ctx.fillText(c.ch, px, py + ch / 2 + 0.5);
      }
    }
  }
}
