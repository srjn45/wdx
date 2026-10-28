package update

import (
	"cmp"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// version is a release version, X.Y.Z with an optional pre-release such as
// dev.4, or a build between releases as git describe names it,
// 0.2.0-dev.4-3-gabc1234: the release and the commits since.
type version struct {
	core    [3]int
	pre     []string
	commits int
}

var versionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.]+?))?(?:-(\d+)-g[0-9a-f]+)?(?:-dirty)?$`)

// parseVersion returns the version s names; ok is false for a build that is
// no release, such as "dev".
func parseVersion(s string) (v version, ok bool) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return version{}, false
	}
	for i := range v.core {
		v.core[i], _ = strconv.Atoi(m[i+1])
	}
	if m[4] != "" {
		v.pre = strings.Split(m[4], ".")
	}
	v.commits, _ = strconv.Atoi(m[5])
	return v, true
}

// newer reports whether release a is newer than b. A build that is no
// release is older than every release.
func newer(a, b string) bool {
	va, okA := parseVersion(a)
	vb, okB := parseVersion(b)
	if !okA || !okB {
		return okA && !okB
	}
	return compare(va, vb) > 0
}

// compare orders versions as Semantic Versioning does, a build after the
// release it was built on.
func compare(a, b version) int {
	if c := slices.Compare(a.core[:], b.core[:]); c != 0 {
		return c
	}
	if c := comparePre(a.pre, b.pre); c != 0 {
		return c
	}
	return cmp.Compare(a.commits, b.commits)
}

// comparePre orders pre-releases: none comes after any, and identifiers
// compare numerically when both are numbers.
func comparePre(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	for i := range min(len(a), len(b)) {
		na, errA := strconv.Atoi(a[i])
		nb, errB := strconv.Atoi(b[i])
		var c int
		switch {
		case errA == nil && errB == nil:
			c = cmp.Compare(na, nb)
		case errA == nil:
			c = -1
		case errB == nil:
			c = 1
		default:
			c = strings.Compare(a[i], b[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}
