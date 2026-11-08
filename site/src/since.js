// What a since badge says: "since 0.5.0" once a stable release has the
// feature, "unreleased" until then. latest is the newest stable release, as
// latestRelease() reads it from the tags.
export function sinceLabel(version, latest) {
  if (!/^\d+\.\d+\.\d+$/.test(version)) throw new Error(`since: ${version} is not a version like 0.5.0`);
  return newer(version, latest) ? 'unreleased' : `since ${version}`;
}

function newer(a, b) {
  const [x, y] = [a, b].map((v) => v.split('.').map(Number));
  for (let i = 0; i < 3; i++) if (x[i] !== y[i]) return x[i] > y[i];
  return false;
}
