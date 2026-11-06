"""Copies a recording from out/<name> to ../<version>/<name>, as tests would read it.

usage: [VERSION=1.5.2] [AGY_REC_OUT=dir] copy.py <name>

Screens: raw.out (and raw2.out after a restart) without script's header line and
what came after the last timed read, with the timing and marks. A transcript
recording's brain/ and hooks.jsonl are copied too. Everything is masked, keeping
lengths so the screen's layout holds: the username, the account's email (agy shows
it in its header) and plan, even when a narrow screen cuts it short, and every UUID,
the same UUID always becoming the same mask. Replay the result and look for anything
private before committing it.
"""
import getpass, os, re, shutil, subprocess, sys

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.environ.get('AGY_REC_OUT', os.path.join(HERE, 'out'))
USER = getpass.getuser().encode()
UUID = re.compile(rb"[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}")
EMAIL = re.compile(rb"[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[a-z]{2,}")
PLAN = re.compile(rb"\(Google AI [A-Za-z ]*")
uuids = {}


def mask(data):
    def uuid(m):
        n = uuids.setdefault(m.group(), len(uuids) + 1)
        return b"00000000-0000-0000-0000-%012d" % n
    out = data.replace(USER, b"x" * len(USER))
    out = EMAIL.sub(lambda m: b"u" * (len(m.group()) - 12) + b"@example.com", out)
    out = PLAN.sub(lambda m: b"(" + b"x" * (len(m.group()) - 1), out)
    out = UUID.sub(uuid, out)
    assert len(out) == len(data), "a mask changed the length"
    return out


def main():
    name = sys.argv[1]
    src = os.path.join(OUT, name)
    dest = os.path.join(HERE, '..', os.environ.get('VERSION', '1.5.2'), name)
    shutil.rmtree(dest, ignore_errors=True)
    os.makedirs(dest)
    for raw_name in ('raw', 'raw2'):
        if not os.path.exists(f"{src}/{raw_name}.out"):
            continue
        raw = open(f"{src}/{raw_name}.out", "rb").read()
        raw = raw[raw.index(b"\n") + 1:]
        timing = open(f"{src}/{raw_name}.timing").readlines()
        raw = raw[:sum(int(line.split()[1]) for line in timing)]
        open(f"{dest}/{raw_name}.out", "wb").write(mask(raw))
        open(f"{dest}/{raw_name}.timing", "w").writelines(timing)
    shutil.copy(f"{src}/marks.log", f"{dest}/marks.log")
    for root, _, files in os.walk(f"{src}/brain"):
        for f in files:
            p = os.path.join(root, f)
            q = mask(os.path.relpath(p, src).encode()).decode()
            os.makedirs(os.path.dirname(os.path.join(dest, q)), exist_ok=True)
            open(os.path.join(dest, q), "wb").write(mask(open(p, "rb").read()))
    if os.path.exists(f"{src}/hooks.jsonl"):
        open(f"{dest}/hooks.jsonl", "wb").write(mask(open(f"{src}/hooks.jsonl", "rb").read()))


if __name__ == "__main__":
    main()
