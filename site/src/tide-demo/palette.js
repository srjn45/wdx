// tide's colours, as internal/style draws them in its dark theme, and the text
// segments every pane is drawn from.

export const P = {
  bg: '#232834', fbg: '#1e222a', head: '#363d4e', tint: '#2b313f', sel: '#264f78',
  fg: '#d5dae4', dim: '#7d879b', line: '#5a6379', green: '#6ccf7f', cyan: '#5cc6d0',
  yellow: '#e2b75e', red: '#e2727b', blue: '#6cb2f0', mag: '#c792ea', orange: '#e39a5f', white: '#f3f5f9',
  addBg: '#23382d', delBg: '#3b2629',
};
export const seg = (t, fg, b, bg) => ({ t, fg, b, bg });
export const len = (t) => Array.from(t).length;
export const segsLen = (ss) => ss.reduce((n, s) => n + len(s.t), 0);
export const tabs4 = (t) => t.replace(/\t/g, '    ');
