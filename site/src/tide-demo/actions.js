// TideSim's state changes: tabs, focus, zoom, the menu, agents and terminals,
// and the demo shell's few commands. Mixed into TideSim by sim.js.
import { P, seg } from './palette.js';
import { FILES, OLD_FILES, ACA, ACA_NAMES, FORM_MODELS, header, prompt, say, sub } from './fixture.js';
import { buildRows, selectable } from './rows.js';

export class Actions {
  /* ---------- state changes ---------- */
  agent(id) { return this.s.agents.find((a) => a.id === id); }
  note(msg) { this.s.notice = msg; this.s.noticeUntil = this.t + 3200; this.dirty = true; }
  openTab(tab, focus) {
    const s = this.s, key = tab.kind + ':' + tab.ref;
    let i = s.tabs.findIndex((t) => t.kind + ':' + t.ref === key);
    if (i < 0) { s.tabs.push(Object.assign({ scroll: 0 }, tab)); i = s.tabs.length - 1; }
    s.active = i; if (focus) s.focus = 'right';
    this.dirty = true;
  }
  closeTab(i) {
    const s = this.s; if (i < 0 || i >= s.tabs.length) return;
    s.tabs.splice(i, 1); if (s.active >= s.tabs.length) s.active = s.tabs.length - 1;
    this.dirty = true;
  }
  toggleZoom(k) { const s = this.s; s.zoom = s.zoom ? null : (k || s.focus); if (s.zoom) s.focus = s.zoom; this.dirty = true; }
  toggleTerm() {
    const s = this.s; s.termOpen = !s.termOpen;
    if (!s.termOpen && s.focus === 'term') s.focus = s.lastTop;
    if (s.termOpen) s.focus = 'term';
    if (s.zoom === 'term') s.zoom = null;
    this.dirty = true;
  }
  moveFocus(dir) {
    const s = this.s; if (s.zoom) return;
    const from = s.focus;
    if (dir === 'left' && from === 'right') s.focus = 'left';
    else if (dir === 'right' && from === 'left') s.focus = 'right';
    else if (dir === 'down' && from !== 'term' && s.termOpen) s.focus = 'term';
    else if (dir === 'up' && from === 'term') s.focus = s.lastTop;
    if (s.focus !== 'term') s.lastTop = s.focus;
    this.dirty = true;
  }
  moveCursor(d) {
    const s = this.s, rows = this.rows || buildRows(s);
    let i = s.cursor;
    for (;;) { i += d; if (i < 0 || i >= rows.length) return; if (selectable(rows[i])) break; }
    s.cursor = i; this.dirty = true;
  }
  clickRow(idx, e) {
    const s = this.s, row = this.rows[idx];
    s.focus = 'left'; s.lastTop = 'left';
    if (row.type === 'head') { s.collapsed[row.sec] = !s.collapsed[row.sec]; this.dirty = true; return; }
    s.cursor = idx; this.openRow(row, e && e.dbl);
  }
  openRow(row) {
    const s = this.s;
    switch (row.type) {
      case 'agent': this.openTab({ kind: 'agent', ref: row.a.id }); break;
      case 'plangroup': s.planFold = !s.planFold; break;
      case 'wt': row.w.open = !row.w.open; this.openTab({ kind: 'diff', ref: row.w.name }); break;
      case 'wtfile': if (OLD_FILES[row.f.path] && row.w.name === 'main') this.openTab({ kind: 'filediff', ref: 'main/' + row.f.path }); else this.openTab({ kind: 'diff', ref: row.w.name }); break;
      case 'docfolder': row.fo.open = !row.fo.open; break;
      case 'doc': this.openTab({ kind: 'doc', ref: row.name }); break;
      case 'planswitch': s.planView = (s.planView + 1) % 3; this.note('(demo) only one view of plans is filled in'); break;
      case 'plan': if (row.p.tasks.length) this.openTab({ kind: 'plan', ref: row.p.name }); else this.note('search is a draft: r runs it'); break;
      case 'codeswitch': s.codeAt = (s.codeAt + 1) % 3; break;
      case 'dir': row.n.open = !row.n.open; if (row.path.startsWith('.worktrees')) this.note('(demo) worktrees are listed above'); break;
      case 'file': if (FILES[row.path]) this.openTab({ kind: 'file', ref: row.path }); break;
    }
    this.dirty = true;
  }
  expand(open) {
    const row = this.rows[this.s.cursor]; if (!row) return;
    if (row.type === 'dir') row.n.open = open;
    else if (row.type === 'wt') row.w.open = open;
    else if (row.type === 'docfolder') row.fo.open = open;
    else if (row.type === 'plangroup') this.s.planFold = !open;
    this.dirty = true;
  }
  answer(a, i) {
    a.prompt = null; a.state = 'working'; a.started = this.t;
    if (i === 2) {
      a.lines.push([seg('  ✗ not run', P.red)]);
      a.stream = [say(a, 'Stopped. What should I do instead?')];
    } else {
      a.lines.push([seg('  ✓ ' + (i === 1 ? 'allowed from now on' : 'allowed once'), P.green)]);
      a.stream = [sub('ok  shop/internal/auth  0.38s', P.green), say(a, 'All tests pass. fix-auth is ready for review.', P.green)];
    }
    a.done = 'idle'; a.next = this.t + 900;
    this.note(a.name + ': answered, and working again');
    this.dirty = true;
  }
  newAgent(aca, form) {
    const s = this.s, name = (form && form.name) || s.newNames.shift() || 'agent-' + s.agents.length;
    const model = form ? FORM_MODELS[aca][form.model][0] : ACA[aca].model;
    const a = { id: name, name, aca, model, state: 'working', ctx: '4k', input: '', started: this.t, next: this.t + 1200, idHex: 'd2f81c6b', wt: (form && form.worktree) || undefined, parent: form && form.parent };
    a.lines = header(a).concat([[seg('(demo) a new ' + ACA_NAMES[aca] + ' session in the project; it would start here.', P.dim)], []]);
    a.stream = [say(a, 'Ready. What should I work on?')]; a.done = 'idle';
    s.agents.splice(s.agents.filter((x) => !x.plan).length, 0, a);
    this.openTab({ kind: 'agent', ref: a.id }, true);
    this.note('created agent ' + name + ' (' + ACA_NAMES[aca] + ')');
  }
  newTerminal() {
    const s = this.s, n = 'shell-' + (s.terms.length + 1);
    s.terms.push({ name: n, lines: [], input: '', running: null, state: 'running', scroll: 0 });
    s.termActive = s.terms.length - 1; if (!s.termOpen) s.termOpen = true; s.focus = 'term';
    this.note('created terminal ' + n); this.dirty = true;
  }
  quit() { this.s.quit = true; this.s.popup = null; this.dirty = true; }
  unquit() { this.s.quit = false; this.dirty = true; }

  /* popups */
  openMenu() {
    const s = this.s, row = this.rows && this.rows[s.cursor], t = s.tabs[s.active];
    const sections = [];
    const tab = { k: '-', label: 'close tab', act: () => this.closeTab(s.active) };
    if (s.focus === 'left' && row) {
      if (row.type === 'agent') sections.push({ title: row.a.name + ' (agent)', items: [{ k: 'i', label: 'info', act: () => this.openTab({ kind: 'info', ref: row.a.id }, true) }, { k: 'c', label: 'new child', act: () => this.agentForm(row.a.name) }, { k: 'r', label: 'rename', act: () => this.note('(demo) r renames it') }, { k: 'R', label: 'new session', act: () => this.note('(demo) R starts a new session') }, { k: 'x', label: 'remove', act: () => this.note('(demo) x removes it, asking first') }] });
      else if (row.type === 'wt' || row.type === 'wtfile') sections.push({ title: (row.w.name) + ' (worktree)', items: [{ k: 'o', label: 'open and go', act: () => this.openRow(row) }, { k: 'R', label: 'rebase', act: () => this.note('(demo) R rebases it onto main') }, { k: 'x', label: 'remove', act: () => this.note('(demo) x removes it once it has landed') }, tab] });
      else if (row.type === 'file' || row.type === 'dir') sections.push({ title: row.path + (row.type === 'dir' ? ' (directory)' : ' (file)'), items: [{ k: 'e', label: 'edit', act: () => this.note('(demo) e opens it in your editor, in a tab') }, { k: 'n', label: 'new file', act: () => this.note('(demo) n creates a file here') }, { k: 'N', label: 'new directory', act: () => this.note('(demo) N creates a directory') }, { k: 'r', label: 'rename', act: () => this.note('(demo) r renames it') }] });
      sections.push({ title: 'list', items: [{ k: '/', label: 'filter', act: () => this.startFilter() }, { k: 'f', label: 'refresh', act: () => this.note('read git again') }, { k: 'z', label: 'collapse section', act: () => this.collapseHere() }] });
    } else if (s.focus === 'right' && t) {
      const items = [tab];
      if (t.kind === 'agent') items.unshift({ k: 'i', label: 'info', act: () => this.openTab({ kind: 'info', ref: t.ref }, true) });
      sections.push({ title: this.tabTitle(t).map((x) => x.t).join('').trim(), items });
    } else if (s.focus === 'term') {
      sections.push({ title: s.terms[s.termActive].name + ' (terminal)', items: [{ k: 'x', label: 'remove', act: () => this.note('(demo) x removes it, asking first') }] });
    }
    sections.push({ title: 'global', items: [{ k: 'a', label: 'new agent', act: () => this.agentForm() }, { k: 't', label: 'new terminal', act: () => this.newTerminal() }, { k: 'p', label: 'project…', act: () => this.projectMenu() }, { k: 's', label: 'settings', act: () => this.openTab({ kind: 'settings', ref: 'settings' }, true) }, { k: 'q', label: 'quit', act: () => this.quit() }] });
    s.popup = { title: 'Menu', sections, sel: 0, foot: 'a letter acts  Enter choose  Esc close' };
    this.dirty = true;
  }
  // The new agent form: name, ACA, model, worktree, permissions and prompt,
  // as TIDE asks them.
  agentForm(parent) {
    this.s.popup = { title: parent ? 'New child of ' + parent : 'New agent in shop-api', form: { field: 0, name: '', aca: 'claude', model: 0, worktree: '', perm: 'full', prompt: '', parent } };
    this.dirty = true;
  }
  formKey(e) {
    const f = this.s.popup.form, k = e.key, acas = Object.keys(FORM_MODELS);
    const textField = { 0: 'name', 3: 'worktree', 5: 'prompt' }[f.field];
    if (k === 'Escape') { this.s.popup = null; return; }
    if (k === 'Enter') { this.s.popup = null; this.newAgent(f.aca, f); return; }
    if (k === 'Tab') { f.field = (f.field + (e.shiftKey ? 5 : 1)) % 6; return; }
    if (k === 'ArrowDown' || k === 'ArrowUp') {
      const d = k === 'ArrowDown' ? 1 : -1, n = FORM_MODELS[f.aca].length;
      if (f.field === 2 && f.model + d >= 0 && f.model + d < n) { f.model += d; return; }
      f.field = Math.max(0, Math.min(5, f.field + d)); return;
    }
    if (k === 'ArrowLeft' || k === 'ArrowRight') {
      const d = k === 'ArrowRight' ? 1 : -1;
      if (f.field === 1) { f.aca = acas[(acas.indexOf(f.aca) + d + acas.length) % acas.length]; f.model = 0; }
      if (f.field === 4) f.perm = f.perm === 'full' ? 'ask' : 'full';
      return;
    }
    if (textField && k === 'Backspace') { f[textField] = f[textField].slice(0, -1); return; }
    if (textField && k.length === 1 && (textField === 'prompt' || /[A-Za-z0-9-]/.test(k))) f[textField] += k;
  }
  projectMenu() {
    this.s.popup = { title: 'Project', sections: [{ items: [{ k: 's', label: 'switch project', act: () => this.note('(demo) one project is open here') }, { k: 'o', label: 'open project', act: () => this.note('(demo) o opens a repository by path or URL') }, { k: 'c', label: 'close project', act: () => this.note('(demo) c closes it; its agents keep running') }] }], sel: 0 };
    this.dirty = true;
  }
  popupItems() { return this.s.popup.sections.flatMap((x) => x.items); }
  runPopup(k) {
    const items = this.popupItems();
    const it = k !== undefined ? items.find((x) => x.k === k) : items[this.s.popup.sel];
    if (!it) return false;
    this.s.popup = null; it.act(); this.dirty = true; return true;
  }
  startFilter() { this.s.filter = { text: '' }; this.s.focus = 'left'; this.dirty = true; }
  collapseHere() {
    const rows = this.rows; let i = this.s.cursor;
    while (i >= 0 && rows[i].type !== 'head') i--;
    if (i >= 0) { this.s.collapsed[rows[i].sec] = !this.s.collapsed[rows[i].sec]; this.s.cursor = i + 1 < rows.length ? i + 1 : i; }
    this.dirty = true;
  }

  /* shell */
  runShell(tm, cmd) {
    tm.lines.push(prompt().concat([seg(cmd)]));
    const out = (t, fg) => tm.lines.push([seg(t, fg)]);
    const c = cmd.trim();
    if (!c) return;
    if (c === 'clear') { tm.lines = []; return; }
    if (c === 'wdx agent list') {
      out('NAME              AI CLI       STATE    ACTIVITY  CONTEXT  WORKTREE', P.dim);
      this.s.agents.forEach((a) => out(a.name.padEnd(18) + (a.aca === 'claude' ? 'claude_code' : a.aca).padEnd(13) + 'running  ' + (a.state === 'waiting' ? 'waiting' : a.state).padEnd(10) + a.ctx.padEnd(9) + (a.wt || 'main'), a.state === 'waiting' ? P.yellow : P.fg));
    } else if (c === 'wdx worktree list') {
      out('NAME            BRANCH          AHEAD  UNCOMMITTED', P.dim);
      this.s.worktrees.forEach((w) => out(w.name.padEnd(16) + w.name.padEnd(16) + String(w.ahead).padEnd(7) + w.files.length + ' files'));
    } else if (c === 'wdx agent list --json' || c.startsWith('wdx') && c.includes('--json')) {
      out('{"agents": [{"name": "reviewer", "aca": "claude_code", "state": "running",', P.fg);
      out('  "activity": "working", "context": {"tokens": 82113}, ...}]}', P.fg);
    } else if (c === 'git log --oneline') {
      out('06f103f (HEAD -> main) feat: cart and checkout skeleton', P.fg);
    } else if (c === 'ls') out('README.md  cmd  go.mod  internal  web', P.cyan);
    else if (c === 'wdx tide') out('TIDE is already open: you are in it', P.dim);
    else if (c === 'go run ./cmd/server') { tm.running = 'server'; out('listening on :8080'); }
    else if (c === 'help' || c === 'wdx') { out('demo shell. try: wdx agent list · wdx worktree list · wdx agent list --json · ls · clear', P.dim); }
    else out('(demo) ' + c.split(' ')[0] + ': this shell knows only a few commands; type help', P.dim);
  }
}
