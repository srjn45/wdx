// A grid of character cells. Box-drawing glyphs are kept as arms (up, down,
// left, right; 1 light, 2 heavy), so where borders meet the right junction is
// drawn, in any font.
import { P } from './palette.js';

export const BOX = {};
(function () {
  const def = (chars, arms) => { for (const ch of chars) BOX[ch] = arms; };
  // u d l r, 1 light 2 heavy
  def('─', [0, 0, 1, 1]); def('│', [1, 1, 0, 0]); def('┌╭', [0, 1, 0, 1]); def('┐╮', [0, 1, 1, 0]);
  def('└╰', [1, 0, 0, 1]); def('┘╯', [1, 0, 1, 0]); def('├', [1, 1, 0, 1]); def('┤', [1, 1, 1, 0]);
  def('┬', [0, 1, 1, 1]); def('┴', [1, 0, 1, 1]); def('┼', [1, 1, 1, 1]);
  def('━', [0, 0, 2, 2]); def('┃', [2, 2, 0, 0]); def('┏', [0, 2, 0, 2]); def('┓', [0, 2, 2, 0]);
  def('┗', [2, 0, 0, 2]); def('┛', [2, 0, 2, 0]); def('┣', [2, 2, 0, 2]); def('┫', [2, 2, 2, 0]);
})();

export class Grid {
  constructor(w, h) {
    this.w = w; this.h = h; this.cells = new Array(w * h);
    for (let i = 0; i < w * h; i++) this.cells[i] = { ch: ' ', fg: P.fg, bg: P.bg, b: false, u: 0, d: 0, l: 0, r: 0, lc: P.line, lw: 0 };
  }
  cell(x, y) { return x >= 0 && y >= 0 && x < this.w && y < this.h ? this.cells[y * this.w + x] : null; }
  fill(x, y, w, h, bg) {
    for (let j = y; j < y + h; j++) for (let i = x; i < x + w; i++) { const c = this.cell(i, j); if (c) c.bg = bg; }
  }
  text(x, y, str, fg, bg, b, maxX) {
    for (const ch of Array.from(str)) {
      if (maxX !== undefined && x >= maxX) break;
      const c = this.cell(x, y);
      if (c) { c.ch = ch; c.fg = fg || P.fg; if (bg) c.bg = bg; c.b = !!b; c.u = c.d = c.l = c.r = 0; c.lw = 0; }
      x++;
    }
    return x;
  }
  segs(x, y, ss, maxX, bg) {
    for (const s of ss) { if (!s) continue; x = this.text(x, y, s.t, s.fg, s.bg || bg, s.b, maxX); if (maxX !== undefined && x >= maxX) break; }
    return x;
  }
  arm(x, y, dir, wt, col) {
    const c = this.cell(x, y); if (!c) return;
    if (c.lw === 0 && c.ch !== ' ') c.ch = ' ';
    c[dir] = Math.max(c[dir], wt);
    if (wt >= c.lw) { c.lw = wt; c.lc = col; }
  }
  hline(x0, x1, y, wt, col) {
    for (let x = x0; x <= x1; x++) { if (x > x0) this.arm(x, y, 'l', wt, col); if (x < x1) this.arm(x, y, 'r', wt, col); }
  }
  vline(x, y0, y1, wt, col) {
    for (let y = y0; y <= y1; y++) { if (y > y0) this.arm(x, y, 'u', wt, col); if (y < y1) this.arm(x, y, 'd', wt, col); }
  }
  box(x, y, w, h, wt, col) {
    this.hline(x, x + w - 1, y, wt, col); this.hline(x, x + w - 1, y + h - 1, wt, col);
    this.vline(x, y, y + h - 1, wt, col); this.vline(x + w - 1, y, y + h - 1, wt, col);
  }
}
