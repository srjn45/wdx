"""Copies the session files a recording kept, from out/<name>/sessions and
out/<name>/first-run, to ../<version>/<name>/, as the tests read them.

usage: [VERSION=0.163.0] [CODEX_REC_OUT=dir] copysessions.py <name>

Each line is rewritten in Codex's own compact JSON, with what is private left out:
the account and user ids, every encrypted blob, whether a field or inside a
string, the instructions text (the base instructions, the developer messages and
the skills list, which name the user's plugins and home), the command prefixes
the user approved, the rate limits and plan, the home and work directories, and
the timezone. Ids, timestamps and the
order of lines stay, so a first-run copy is still a prefix of its later one. A
first-run file the same as its later one is not copied. Look at the result for
anything private before committing it.
"""
import getpass, glob, json, os, re, shutil, sys

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.environ.get('CODEX_REC_OUT', os.path.join(HERE, 'out'))
LEFT_OUT = '(left out)'
HOME = os.path.expanduser('~')
USER = getpass.getuser()
# rec.py's work directories, /tmp/codexrec/w<random>, as step 3's sample has it.
WORK = re.compile(r'/tmp/codexrec/w[a-z0-9_]+')
TIMEZONE = re.compile(r'\b[A-Z][a-z]+/[A-Z][A-Za-z_]+\b')
# An encrypted message inside a string, such as a subagent's task in a tool call's arguments.
FERNET = re.compile(r'gAAAAA[A-Za-z0-9_=-]+')


def scrub(v, key=None):
    if isinstance(v, dict):
        if v.get('role') == 'developer' and isinstance(v.get('content'), list):
            v = dict(v, content=[dict(c, text=LEFT_OUT) if 'text' in c else c for c in v['content']])
        out = {}
        for k, x in v.items():
            if k in ('creator_user_id', 'creator_account_id', 'base_instructions', 'encrypted_content', 'git'):
                out[k] = LEFT_OUT
            elif k == 'approved_command_prefixes':
                out[k] = []
            elif k == 'rate_limits':
                out[k] = None
            elif k == 'host_skills' and isinstance(x, dict) and 'body' in x:
                out[k] = dict(x, body=LEFT_OUT)
            else:
                out[scrub(k)] = scrub(x, k)
        return out
    if isinstance(v, list):
        return [scrub(x) for x in v]
    if isinstance(v, str):
        v = WORK.sub('/work/repo', v).replace(HOME, '/home/x').replace(USER, 'x')
        v = FERNET.sub(LEFT_OUT, v)
        return TIMEZONE.sub('UTC', v) if key == 'timezone' or '<timezone>' in v else v
    return v


def copy(src, dest):
    with open(src) as f, open(dest, 'w') as out:
        for line in f:
            out.write(json.dumps(scrub(json.loads(line)), separators=(',', ':'), ensure_ascii=False) + '\n')


def main():
    name = sys.argv[1]
    src = os.path.join(OUT, name)
    dest = os.path.join(HERE, '..', os.environ.get('VERSION', '0.163.0'), name)
    shutil.rmtree(dest, ignore_errors=True)
    for part in ('sessions', 'first-run'):
        os.makedirs(f'{dest}/{part}', exist_ok=True)
        for path in sorted(glob.glob(f'{src}/{part}/*.jsonl')):
            base = os.path.basename(path)
            later = f'{src}/sessions/{base}'
            if part == 'first-run' and open(path, 'rb').read() == open(later, 'rb').read():
                continue
            copy(path, f'{dest}/{part}/{base}')


if __name__ == '__main__':
    main()
