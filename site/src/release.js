import { execFileSync } from 'node:child_process';

// The newest stable release, read from the tags at build time: the page shows
// it, the install command fetches it and the TIDE demo's status line names it.
// A checkout without tags fails the build rather than show a stale version.
export function latestRelease() {
  const tags = execFileSync('git', ['tag', '--list', 'v*', '--sort=-v:refname'], { encoding: 'utf8' }).split('\n');
  const stable = tags.find((t) => /^v\d+\.\d+\.\d+$/.test(t));
  if (!stable) throw new Error('no stable release among the tags: run git fetch --tags');
  return stable.slice(1);
}

export const installCommand = 'curl -fsSL https://raw.githubusercontent.com/srjn45/wdx/main/scripts/install.sh | sh';
