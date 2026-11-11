// wdx's releases up to v0.5.0, counted from git for the post "I built wdx with
// wdx": the day of each tag since the first commit (2026-10-26), the commits
// since the release before, and the Go lines in the tree and added since the
// release before, generated code (*.gen.go) left out.
export const RELEASES = [
  { tag: 'v0.1.0', date: '2026-10-28', day: 2, commits: 32, goLines: 11064, goAdded: 11012 },
  { tag: 'v0.2.0', date: '2026-11-01', day: 6, commits: 185, goLines: 50104, goAdded: 41208 },
  { tag: 'v0.3.0', date: '2026-11-03', day: 8, commits: 76, goLines: 85421, goAdded: 36124 },
  { tag: 'v0.4.0', date: '2026-11-07', day: 12, commits: 190, goLines: 129069, goAdded: 45919 },
  { tag: 'v0.5.0', date: '2026-11-11', day: 16, commits: 220, goLines: 185168, goAdded: 63019 },
];

// The first commit's tree, day 0.
export const FIRST = { day: 0, goLines: 64 };

export const fmt = (n) => n.toLocaleString('en-US');
export const compact = (n) => (n === 0 ? '0' : `${n / 1000}K`);
