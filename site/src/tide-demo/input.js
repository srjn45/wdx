// TideSim's input: mouse, wheel and keys, mapped to tide's bindings. The
// menu is Ctrl+Space as well as C-q, as Firefox on Linux quits on C-q before
// the page sees it. Mixed into TideSim by sim.js.
import { P, seg } from './palette.js';
import { ACA_NAMES, prompt, user, say, sub } from './fixture.js';
import { selectable } from './rows.js';

export class Input {
  /* ---------- input ---------- */
  bind() {
    const el = this.canvas;
    const cellAt = (e) => { const r = el.getBoundingClientRect(); return [Math.floor((e.clientX - r.left) / (r.width / this.W)), Math.floor((e.clientY - r.top) / (r.height / this.H))]; };
    el.addEventListener('mousedown', (e) => {
      e.preventDefault();
      this.host.focus({ preventScroll: true });
      this.userAct();
      const [cx, cy] = cellAt(e);
      let inModal = false;
      for (let i = this.hits.length - 1; i >= 0; i--) {
        const h = this.hits[i];
        if (h.modal) { const [a, b, c, d] = h.modal; if (cx < a || cx > c || cy < b || cy > d) { this.s.popup = null; this.dirty = true; return; } inModal = true; continue; }
        if (h.fn && cx >= h.x0 && cx <= h.x1 && cy >= h.y0 && cy <= h.y1) { h.fn(e); this.dirty = true; return; }
      }
      if (inModal) return;
      const pane = this.hits.find((h) => h.wheel && cx >= h.x0 && cx <= h.x1 && cy >= h.y0 && cy <= h.y1);
      if (pane) { this.s.focus = pane.wheel; if (pane.wheel !== 'term') this.s.lastTop = pane.wheel; this.dirty = true; }
    });
    el.addEventListener('wheel', (e) => {
      if (!this.active) return;
      const [cx, cy] = cellAt(e);
      const pane = this.hits.find((h) => h.wheel && cx >= h.x0 && cx <= h.x1 && cy >= h.y0 && cy <= h.y1);
      if (!pane) return;
      e.preventDefault();
      const d = e.deltaY > 0 ? 3 : -3, s = this.s;
      if (pane.wheel === 'left') { s.lscroll = Math.max(0, s.lscroll + d); const rows = this.rows; const listH = this.lay.left.h - 2; if (s.cursor < s.lscroll) s.cursor = Math.min(rows.length - 1, s.lscroll); if (s.cursor >= s.lscroll + listH) s.cursor = s.lscroll + listH - 1; while (rows[s.cursor] && !selectable(rows[s.cursor])) s.cursor++; }
      else if (pane.wheel === 'right') { const t = s.tabs[s.active]; if (t) t.scroll = (t.scroll || 0) + (t.kind === 'agent' ? -d : d); }
      else { const tm = s.terms[s.termActive]; tm.scroll = Math.max(0, (tm.scroll || 0) - d); }
      this.dirty = true;
    }, { passive: false });
    this.host.addEventListener('focus', () => { this.active = true; this.dirty = true; this.emit('active', true); });
    this.host.addEventListener('blur', () => { this.active = false; this.dirty = true; this.emit('active', false); });
    this.host.addEventListener('keydown', (e) => { if (this.key(e)) { e.preventDefault(); e.stopPropagation(); this.dirty = true; } });
    if (window.ResizeObserver) new ResizeObserver(() => { if (!this.opts.fixed) this.resize(); }).observe(this.host);
    if (window.IntersectionObserver) new IntersectionObserver((en) => { this.visible = en[0].isIntersecting; }).observe(this.host);
    this.tick();
  }
  userAct() { if (this.playing) { this.stopPlay(); this.emit('drive'); } }

  key(e) {
    const s = this.s, k = e.key;
    this.userAct();
    if (s.quit) { if (k === 'Enter' || k === ' ') { this.unquit(); return true; } return false; }
    if (e.ctrlKey && (k === ' ' || k === 'q' || k === 'Q')) { if (s.popup) s.popup = null; else this.openMenu(); return true; }
    if (s.popup) {
      const n = this.popupItems().length;
      if (k === 'Escape') { s.popup = null; return true; }
      if (k === 'ArrowDown' || k === 'j') { s.popup.sel = (s.popup.sel + 1) % n; return true; }
      if (k === 'ArrowUp' || k === 'k') { s.popup.sel = (s.popup.sel + n - 1) % n; return true; }
      if (k === 'Enter') { this.runPopup(); return true; }
      if (k.length === 1) { this.runPopup(k); return true; }
      return true;
    }
    if (e.altKey && k.startsWith('Arrow')) { this.moveFocus(k.slice(5).toLowerCase()); return true; }
    if (e.ctrlKey && k === ']') { this.toggleZoom(); return true; }
    if (e.ctrlKey && k === '\\') { this.toggleTerm(); return true; }
    if (e.ctrlKey && (k === 'c' || k === 'C') && s.focus === 'term') { const tm = s.terms[s.termActive]; if (tm.running) { tm.running = null; tm.lines.push([seg('^C', P.dim)]); } else { tm.lines.push(prompt().concat([seg(tm.input + '^C', P.dim)])); tm.input = ''; } return true; }
    if (e.ctrlKey || e.metaKey || e.altKey) return false;
    if (s.focus === 'left') return this.keyLeft(e);
    if (s.focus === 'right') return this.keyRight(e);
    return this.keyTerm(e);
  }
  keyLeft(e) {
    const s = this.s, k = e.key;
    if (s.filter) {
      if (k === 'Escape') { s.filter = null; return true; }
      if (k === 'Enter') { const t = s.filter.text; s.filter = t ? s.filter : null; return true; }
      if (k === 'Backspace') { s.filter.text = s.filter.text.slice(0, -1); return true; }
      if (k === 'ArrowDown') { this.moveCursor(1); return true; }
      if (k === 'ArrowUp') { this.moveCursor(-1); return true; }
      if (k.length === 1) { s.filter.text += k; s.cursor = 1; return true; }
      return true;
    }
    switch (k) {
      case 'ArrowDown': case 'j': this.moveCursor(1); return true;
      case 'ArrowUp': case 'k': this.moveCursor(-1); return true;
      case 'ArrowRight': case 'l': this.expand(true); return true;
      case 'ArrowLeft': case 'h': this.expand(false); return true;
      case 'Enter': { const r = this.rows[s.cursor]; if (r) this.openRow(r); return true; }
      case 'Escape': return true;
      case 'a': this.agentForm(); return true;
      case 't': this.newTerminal(); return true;
      case 'p': this.projectMenu(); return true;
      case 's': this.openTab({ kind: 'settings', ref: 'settings' }, true); return true;
      case 'q': this.quit(); return true;
      case 'z': this.collapseHere(); return true;
      case '/': this.startFilter(); return true;
      case 'f': this.note('read git again'); return true;
      case 'i': { const r = this.rows[s.cursor]; if (r && r.type === 'agent') this.openTab({ kind: 'info', ref: r.a.id }, true); return true; }
      case '-': this.closeTab(s.active); return true;
      case '?': this.openMenu(); return true;
    }
    if (k.length === 1) { this.note('(demo) ' + k + ' does nothing here: Ctrl+Space shows the keys'); return true; }
    return false;
  }
  keyRight(e) {
    const s = this.s, k = e.key, t = s.tabs[s.active];
    if (!t) return false;
    if (t.kind === 'agent') {
      const a = this.agent(t.ref);
      if (a.state === 'waiting' && a.prompt) {
        const n = a.prompt.opts.length;
        if (k === 'ArrowDown') { a.prompt.sel = (a.prompt.sel + 1) % n; return true; }
        if (k === 'ArrowUp') { a.prompt.sel = (a.prompt.sel + n - 1) % n; return true; }
        if (k === 'Enter') { this.answer(a, a.prompt.sel); return true; }
        if (/^[1-9]$/.test(k) && +k <= n) { a.prompt.sel = +k - 1; this.answer(a, +k - 1); return true; }
        return true;
      }
      if (k === 'Enter') {
        const txt = a.input.trim(); a.input = '';
        if (!txt) return true;
        a.lines.push([]); a.lines.push(user(txt));
        a.state = 'working'; a.started = this.t; a.next = this.t + 1400; a.done = 'idle';
        a.stream = [say(a, '(demo) In wdx this goes to the real ' + ACA_NAMES[a.aca] + ' session, which answers here.'), sub('wdx agent send --name ' + a.name + ' --prompt "…" does the same from a script')];
        return true;
      }
      if (k === 'Backspace') { a.input = a.input.slice(0, -1); return true; }
      if (k === 'Escape') { if (a.state === 'working' && a.stream) { a.stream = []; a.state = 'idle'; a.lines.push([seg('  interrupted', P.dim)]); } return true; }
      if (k.length === 1) { a.input += k; return true; }
      return false;
    }
    if (t.kind === 'plan') {
      const p = s.plans.find((x) => x.name === t.ref);
      if (k === 'ArrowDown' || k === 'j') { p.sel = Math.min(p.tasks.length - 1, p.sel + 1); return true; }
      if (k === 'ArrowUp' || k === 'k') { p.sel = Math.max(0, p.sel - 1); return true; }
      if (k === 'Enter') { const tk = p.tasks[p.sel]; if (tk.agent) this.openTab({ kind: 'agent', ref: tk.agent }, true); return true; }
      if (k === 'p') { this.note('(demo) p pauses the plan: running tasks finish'); return true; }
    }
    if (k === '-') { this.closeTab(s.active); return true; }
    if (k === 'ArrowDown' || k === 'j') { t.scroll = (t.scroll || 0) + 1; if (t.kind === 'file') t.line = (t.line || 0) + 1; return true; }
    if (k === 'ArrowUp' || k === 'k') { t.scroll = Math.max(0, (t.scroll || 0) - 1); if (t.kind === 'file') t.line = Math.max(0, (t.line || 0) - 1); return true; }
    if (k === 'PageDown' || k === ' ') { t.scroll = (t.scroll || 0) + 10; return true; }
    if (k === 'PageUp') { t.scroll = Math.max(0, (t.scroll || 0) - 10); return true; }
    if (k === 'L' && t.kind === 'diff') { this.note('(demo) L lands ' + t.ref + ' on main after the landing check'); return true; }
    if (k === 'e') { this.note('(demo) e opens it in your editor, in a tab'); return true; }
    return false;
  }
  keyTerm(e) {
    const s = this.s, k = e.key, tm = s.terms[s.termActive];
    if (tm.running) {
      if (k === 'c' && e.ctrlKey) return false;
      if (k === 'Enter') { tm.lines.push([]); return true; }
      return k.length === 1;
    }
    if (k === 'Enter') { const c = tm.input; tm.input = ''; this.runShell(tm, c); tm.scroll = 0; return true; }
    if (k === 'Backspace') { tm.input = tm.input.slice(0, -1); return true; }
    if (k.length === 1) { tm.input += k; return true; }
    return false;
  }
}
