// Just enough of a browser for the tide demo to run in node: a canvas whose
// context draws nothing, elements that keep their listeners, and the grid the
// demo would have painted, read back as lines of text.
const ctx = new Proxy({}, { get: (t, k) => (k in t ? t[k] : () => {}), set: (t, k, v) => { t[k] = v; return true; } });

function element() {
  const el = {
    style: {}, listeners: {}, clientWidth: 1180,
    getContext: () => ctx, setAttribute() {}, appendChild(c) { el.child = c; },
    addEventListener(ev, fn) { el.listeners[ev] = fn; },
    focus() { el.listeners.focus?.(); },
    getBoundingClientRect: () => ({ left: 0, top: 0, width: 1180, height: 578 }),
  };
  return el;
}

export function setup() {
  globalThis.window = { devicePixelRatio: 1 };
  globalThis.document = { hidden: false, createElement: element };
}

// Every light and heavy box glyph by its arms: up, down, left, right.
const ARMS = {
  '0011': '─', '0022': '━', '0101': '┌', '0102': '┍', '0110': '┐', '0111': '┬', '0112': '┮', '0120': '┑', '0121': '┭', '0122': '┯', '0201': '┎',
  '0202': '┏', '0210': '┒', '0211': '┰', '0212': '┲', '0220': '┓', '0221': '┱', '0222': '┳', '1001': '└', '1002': '┕', '1010': '┘', '1011': '┴',
  '1012': '┶', '1020': '┙', '1021': '┵', '1022': '┷', '1100': '│', '1101': '├', '1102': '┝', '1110': '┤', '1111': '┼', '1112': '┾', '1120': '┥',
  '1121': '┽', '1122': '┿', '1201': '┟', '1202': '┢', '1210': '┧', '1211': '╁', '1212': '╆', '1220': '┪', '1221': '╅', '1222': '╈', '2001': '┖',
  '2002': '┗', '2010': '┚', '2011': '┸', '2012': '┺', '2020': '┛', '2021': '┹', '2022': '┻', '2101': '┞', '2102': '┡', '2110': '┦', '2111': '╀',
  '2112': '╄', '2120': '┩', '2121': '╃', '2122': '╇', '2200': '┃', '2201': '┠', '2202': '┣', '2210': '┨', '2211': '╂', '2212': '╊', '2220': '┫',
  '2221': '╉', '2222': '╋',
};

// The screen as tide would print it: each cell's character, or the box glyph its arms make.
export function screen(sim) {
  const g = sim.grid, lines = [];
  for (let y = 0; y < g.h; y++) {
    let l = '';
    for (let x = 0; x < g.w; x++) {
      const c = g.cells[y * g.w + x];
      l += c.lw ? ARMS[[c.u, c.d, c.l, c.r].join('')] || '+' : c.ch;
    }
    lines.push(l);
  }
  return lines;
}

export function press(host, key, mod = {}) {
  host.listeners.keydown({ key, ctrlKey: !!mod.ctrl, altKey: !!mod.alt, metaKey: false, preventDefault() {}, stopPropagation() {} });
}

// A click in the middle of cell x, y.
export function click(sim, x, y) {
  const r = sim.canvas.getBoundingClientRect();
  sim.canvas.listeners.mousedown({ clientX: (x + 0.5) * (r.width / sim.W), clientY: (y + 0.5) * (r.height / sim.H), preventDefault() {} });
}
