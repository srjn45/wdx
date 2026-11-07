// The landing page's behaviour: the hero's live tide and its tour, the screens
// and close-ups drawn from the demo's scenes, the dialog that opens a screen
// live, and the install command's copy button.
import { TideSim } from '../tide-demo/sim.js';
import { TOUR } from '../tide-demo/tour.js';

const $ = (id) => document.getElementById(id);
const version = document.querySelector('[data-version]').dataset.version;
const reduceMotion = matchMedia('(prefers-reduced-motion: reduce)').matches;

const SHOTS = [
  { scene: 'agents', cls: 'hero-shot', title: 'Agents at a glance', text: 'Working, waiting or idle, and the context each has used, for every agent and the agents of a running plan.' },
  { scene: 'answer', cls: '', title: 'Answer an agent', text: 'A question from an agent, answered without leaving tide.' },
  { scene: 'plan', cls: '', title: 'Plans', text: 'Tasks drawn as a DAG, each with its agent.' },
  { scene: 'diff', cls: 'wide', title: 'Worktrees and diffs', text: 'What a worktree committed and what it has not, ready to land.' },
  { scene: 'code', cls: 'wide', title: 'Code', text: 'The code tree of any worktree, with git marks.' },
  { scene: 'menu', cls: '', title: 'The C-q menu', text: 'What every key does, wherever you are.' },
  { scene: 'terminal', cls: '', title: 'Terminals', text: 'Shells below the agents, still running when tide closes.' },
  { scene: 'zoom', cls: '', title: 'Zoom', text: 'C-] gives the pane you are in the whole window.' },
];

// The close-ups: a scene and the cells cut from it (x, y, width, height).
const CROPS = [
  { id: 'crop-agents', scene: 'agents', cells: [0, 0, 64, 14], alt: 'tide: the Agents section, with a waiting agent in amber' },
  { id: 'crop-diff', scene: 'diff', cells: [0, 8, 92, 18], alt: 'tide: a worktree and its diff' },
  { id: 'crop-plan', scene: 'plan', cells: [30, 0, 90, 16], alt: 'tide: a plan of four tasks drawn as a graph' },
];

for (const b of document.querySelectorAll('[data-copy]')) {
  b.addEventListener('click', () => {
    const done = (label) => { b.textContent = label; setTimeout(() => (b.textContent = 'Copy'), 1600); };
    navigator.clipboard?.writeText(b.dataset.copy).then(() => done('Copied'), () => done('Select it')) ?? done('Select it');
  });
}

// A scene as a PNG, whole or cut to some of its cells.
function snapshot(scene, cells) {
  const sim = new TideSim(null, { fixed: { W: 120, H: 34, cw: 9, dpr: 2 }, version });
  sim.scene(scene);
  if (!cells) return sim.canvas.toDataURL('image/png');
  const [x, y, w, h] = cells, kx = sim.canvas.width / sim.W, ky = sim.canvas.height / sim.H;
  const c = document.createElement('canvas');
  c.width = w * kx; c.height = h * ky;
  c.getContext('2d').drawImage(sim.canvas, x * kx, y * ky, w * kx, h * ky, 0, 0, w * kx, h * ky);
  return c.toDataURL('image/png');
}

function image(src, alt) {
  const i = new Image();
  i.src = src; i.alt = alt; i.decoding = 'async';
  return i;
}

let hero, dialogSim;

function buildScreens() {
  const bento = $('bento');
  for (const s of SHOTS) {
    const b = document.createElement('button');
    b.type = 'button'; b.className = 'shot ' + s.cls;
    b.innerHTML = '<span class="try">TRY THIS SCREEN</span><span class="img"></span><span class="cap"><b></b><span></span></span>';
    b.querySelector('.img').appendChild(image(snapshot(s.scene), 'tide: ' + s.title));
    b.querySelector('b').textContent = s.title;
    b.querySelector('.cap span').textContent = s.text;
    b.addEventListener('click', () => openScreen(s));
    bento.appendChild(b);
  }
  for (const c of CROPS) $(c.id).appendChild(image(snapshot(c.scene, c.cells), c.alt));
}

function buildHero() {
  hero = new TideSim($('hero-sim'), { rows: 34, maxW: 136, version });
  const title = (z) => { $('hero-title').textContent = `wdx tide · shop-api · ${z.W}×${z.H}`; };
  hero.on('size', title);
  title(hero);
  const bar = $('capbar'), cap = $('hero-sim-cap');
  hero.on('caption', (t) => { if (t) { cap.textContent = t; bar.classList.remove('driving'); } });
  const driving = () => { bar.classList.add('driving'); cap.textContent = "You're driving: click rows and tabs, scroll, type into an agent or a terminal. Ctrl+Space opens the menu."; };
  hero.on('drive', driving);
  $('drive').addEventListener('click', () => { hero.stopPlay(); driving(); $('hero-sim').focus({ preventScroll: true }); });
  $('replay').addEventListener('click', () => hero.play(TOUR));
  if (reduceMotion) {
    hero.scene('agents');
    cap.textContent = 'The tour is paused because your system asks for less motion. Replay it, or take the wheel.';
  } else hero.play(TOUR);
}

const dialog = $('lb');
function openScreen(s) {
  hero?.stopPlay();
  $('lb-title').textContent = 'wdx tide · ' + s.title;
  $('lb-sim-cap').textContent = s.text + ' Click, scroll and type: this window is live.';
  dialog.showModal();
  dialogSim ??= new TideSim($('lb-sim'), { rows: 34, maxW: 140, version });
  dialogSim.resize();
  dialogSim.scene(s.scene);
  $('lb-sim').focus({ preventScroll: true });
}
$('lb-close').addEventListener('click', () => dialog.close());
dialog.addEventListener('click', (e) => { if (e.target === dialog) dialog.close(); });

// The demo measures JetBrains Mono's cells, so it waits for the font, but not
// for long: a page without it still gets the demo in a fallback font.
let built = false;
const build = () => { if (built) return; built = true; buildScreens(); buildHero(); };
Promise.all([document.fonts.load('400 16px "JetBrains Mono"'), document.fonts.load('700 16px "JetBrains Mono"')]).catch(() => {}).then(build);
setTimeout(build, 2500);
