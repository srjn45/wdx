"""Copies a recording from out/<src> to ../2.1.314/<name>, masking what is private.

usage: sanitize.py <src> <name> [word-to-mask ...]

Masks keep each word's length, so the screen's layout holds. A narrow screen can
wrap a path or id across lines, past these masks: replay the result and scan
every screen for the username, the home directory and session ids before
committing it.
"""
import getpass, os, re, shutil, sys

HERE = os.path.dirname(os.path.abspath(__file__))
USER = getpass.getuser().encode()
UUID = re.compile(rb"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")


def mask(word):
    return re.sub(rb"[^/-]", b"x", word)


def clean(raw, words):
    raw = raw.replace(HERE.encode(), mask(HERE.encode()))
    slug = re.sub(r"[^A-Za-z0-9-]", "-", HERE).encode()
    raw = raw.replace(slug, mask(slug))
    for word in [USER, *words]:
        raw = raw.replace(word, mask(word))
    return UUID.sub(lambda m: re.sub(rb"[0-9a-f]", b"0", m.group()), raw)


def main():
    src, name, words = sys.argv[1], sys.argv[2], [w.encode() for w in sys.argv[3:]]
    src = f"{HERE}/out/{src}"
    dest = f"{HERE}/../2.1.314/{name}"
    shutil.rmtree(dest, ignore_errors=True)
    os.makedirs(dest)
    raw = open(f"{src}/raw.out", "rb").read()
    raw = raw[raw.index(b"\n") + 1 :]  # script's header
    lines = [l for l in open(f"{src}/raw.timing") if l[0] in "OI"]
    raw = raw[: sum(int(l.split()[2]) for l in lines if l[0] == "O")]
    cleaned = clean(raw, words)
    assert len(cleaned) == len(raw), "a mask changed the length"
    open(f"{dest}/raw.out", "wb").write(cleaned)
    open(f"{dest}/raw.timing", "w").writelines(lines)
    if os.path.exists(f"{src}/marks.log"):
        shutil.copy(f"{src}/marks.log", f"{dest}/marks.log")


if __name__ == "__main__":
    main()
