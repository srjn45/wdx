"""Copies a recording from out/<name> to ../<version>/<name>, as tests would read it.

usage: [VERSION=2026.11.05] [CURSOR_REC_OUT=dir] copy.py <name>

It drops script's header line, which names the program's path, and what came after the
last timed read (Cursor's goodbye, with its chat id), and masks the username and every
UUID, keeping their length so the screen's layout holds. Replay the result and look
for anything private before committing it.
"""
import getpass, os, re, shutil, sys

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.environ.get('CURSOR_REC_OUT', os.path.join(HERE, 'out'))
USER = getpass.getuser().encode()
UUID = re.compile(rb"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")


def main():
    name = sys.argv[1]
    src = os.path.join(OUT, name)
    dest = os.path.join(HERE, '..', os.environ.get('VERSION', '2026.11.05'), name)
    shutil.rmtree(dest, ignore_errors=True)
    os.makedirs(dest)
    raw = open(f"{src}/raw.out", "rb").read()
    raw = raw[raw.index(b"\n") + 1:]
    timing = open(f"{src}/raw.timing").readlines()
    raw = raw[:sum(int(line.split()[1]) for line in timing)]
    cleaned = UUID.sub(lambda m: re.sub(rb"[0-9a-f]", b"0", m.group()), raw.replace(USER, b"x" * len(USER)))
    assert len(cleaned) == len(raw), "a mask changed the length"
    open(f"{dest}/raw.out", "wb").write(cleaned)
    open(f"{dest}/raw.timing", "w").writelines(timing)
    shutil.copy(f"{src}/marks.log", f"{dest}/marks.log")


if __name__ == "__main__":
    main()
