// Syntax colours for the code tabs and the diff tabs' lines.
import { P, seg, tabs4 } from './palette.js';

const KW = /^(package|import|func|return|type|struct|for|range|if|var|const|nil|go|export|function|from|interface)$/;
const TY = /^(int|string|error|bool|time|http|json|cart|Handler|Cart|Item|Order|Session|Config)$/;
export function hl(line, lang) {
  line = tabs4(line);
  if (lang === 'md') {
    if (/^#/.test(line)) return [seg(line, P.cyan, true)];
    if (/^    /.test(line)) return [seg(line, P.green)];
    if (/^- /.test(line)) return [seg('- ', P.dim), seg(line.slice(2))];
    return [seg(line)];
  }
  const out = []; const re = /(\/\/.*$)|("(?:[^"\\]|\\.)*"|`[^`]*`)|([A-Za-z_][A-Za-z0-9_]*)|(\d+)|(\s+)|(.)/g; let mm;
  while ((mm = re.exec(line))) {
    if (mm[1]) out.push(seg(mm[1], P.dim));
    else if (mm[2]) out.push(seg(mm[2], P.green));
    else if (mm[3]) out.push(seg(mm[3], KW.test(mm[3]) ? P.mag : TY.test(mm[3]) ? P.yellow : P.fg));
    else if (mm[4]) out.push(seg(mm[4], P.orange));
    else out.push(seg(mm[0], P.fg));
  }
  return out;
}
export function diffLines(text) {
  return text.split('\n').map((l, i) => {
    const t = tabs4(l);
    if (i === 0) return [seg(t, P.dim)];
    if (/^(diff --git|index |new file|--- |\+\+\+ )/.test(l)) return [seg(t, P.white, true)];
    if (l.startsWith('@@')) return [seg(t, P.cyan)];
    if (l.startsWith('+')) return [seg(t, P.green, false, P.addBg)];
    if (l.startsWith('-')) return [seg(t, P.red, false, P.delBg)];
    return [seg(t, P.fg)];
  });
}
