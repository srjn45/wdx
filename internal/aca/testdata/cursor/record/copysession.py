"""Copies one chat's files, as Cursor kept them, to ../<version>/session/, with what is private left out.

usage: [VERSION=2026.11.05] [SAMPLE=session] [STORE=<store.db>] copysession.py <cursor config dir> <cursor data dir> <chat id> [hook input file]

SAMPLE names the folder written, ../<version>/<sample>/. STORE reads the messages from
another store.db than the chat's, such as a snapshot taken before a /summarize, into
store-messages-<its file name without .db>.jsonl; the other files are then not written.

It writes:
- transcript.jsonl and subagents/<id>.jsonl: the agent transcript Cursor keeps under
  <data dir>/projects/<slug>/agent-transcripts/<id>/, as it is;
- meta.json: the chat's chats/<md5 of its dir>/<id>/meta.json;
- store-root.txt: the fields of store.db's latest root blob, as read by a generic
  protobuf decoder (field 1 is the messages, in order), and store-messages.jsonl: each
  message blob that field names, in its order; the system prompt and the user_info
  message are left out, and the store's meta without its blobEncryptionKey;
- hook-session-start.json: a sessionStart hook's input, without user_email.
Home and work directories become /home/x and /work/repo. Look at the result for anything
private before committing it.
"""
import getpass, glob, json, os, re, sqlite3, sys

HERE = os.path.dirname(os.path.abspath(__file__))
LEFT_OUT = '(left out)'


def main():
    config, data, chat = sys.argv[1:4]
    hook = sys.argv[4] if len(sys.argv) > 4 else None
    dest = os.path.join(HERE, '..', os.environ.get('VERSION', '2026.11.05'), os.environ.get('SAMPLE', 'session'))
    store = os.environ.get('STORE')
    chat_dir = glob.glob(f'{config}/chats/*/{chat}')[0]
    meta = json.load(open(f'{chat_dir}/meta.json'))
    work, home = meta['cwd'], os.path.dirname(data.rstrip('/'))
    hook_input = json.loads(open(hook).read()) if hook else {}
    works = [work] + hook_input.get('workspace_roots', [])
    def scrub(s):
        for w in works:
            s = s.replace(w, '/work/repo').replace(slug(w), 'work-repo')
        s = s.replace(home, '/home/x').replace(slug(home), 'home-x')
        return s.replace(getpass.getuser(), 'x')
    if store:
        name = os.path.splitext(os.path.basename(store))[0]
        write_messages(sqlite3.connect(f'file:{store}?mode=ro', uri=True), scrub, f'{dest}/store-messages-{name}.jsonl')
        return
    os.makedirs(os.path.join(dest, 'subagents'), exist_ok=True)
    meta['cwd'] = scrub(meta['cwd'])
    json.dump(meta, open(f'{dest}/meta.json', 'w'))
    tdir = glob.glob(f'{data}/projects/*/agent-transcripts/{chat}')[0]
    open(f'{dest}/transcript.jsonl', 'w').write(scrub(open(f'{tdir}/{chat}.jsonl').read()))
    for f in glob.glob(f'{tdir}/subagents/*.jsonl'):
        open(f'{dest}/subagents/{os.path.basename(f)}', 'w').write(scrub(open(f).read()))
    root = write_messages(sqlite3.connect(f'file:{chat_dir}/store.db?mode=ro', uri=True), scrub, f'{dest}/store-messages.jsonl')
    with open(f'{dest}/store-root.txt', 'w') as out:
        for field, value in fields(root):
            shown = value.hex() if isinstance(value, bytes) and len(value) == 32 else (
                f'<{len(value)} bytes>' if isinstance(value, bytes) else value)
            out.write(f'field {field}: {shown}\n')
    if hook:
        h = hook_input
        h.pop('user_email', None)
        json.dump(json.loads(scrub(json.dumps(h))), open(f'{dest}/hook-session-start.json', 'w'))


def write_messages(db, scrub, path):
    """Writes the messages the store's latest root names, in order, and returns the root."""
    blobs = dict(db.execute('select id, data from blobs'))
    store_meta = json.loads(bytes.fromhex(db.execute("select value from meta where key='0'").fetchone()[0]))
    store_meta['blobEncryptionKey'] = LEFT_OUT
    root = blobs[store_meta['latestRootBlobId']]
    with open(path, 'w') as out:
        out.write(json.dumps({'store_meta': store_meta}) + '\n')
        for field, value in fields(root):
            if field == 1:
                m = json.loads(blobs[value.hex()])
                text = json.dumps(m.get('content'))
                if m.get('role') == 'system' or '<user_info>' in text:
                    m['content'] = LEFT_OUT
                m = json.loads(re.sub(r'"signature": "[^"]*"', '"signature": "(left out)"', scrub(json.dumps(m))))
                out.write(json.dumps({'id': value.hex(), 'message': m}) + '\n')
    return root


def slug(path):
    """A path as Cursor names its projects/ folder: every run of other characters a dash."""
    return re.sub(r'[^A-Za-z0-9]+', '-', path).strip('-')


def varint(b, i):
    r = s = 0
    while True:
        c = b[i]; i += 1; r |= (c & 0x7f) << s; s += 7
        if c < 0x80:
            return r, i


def fields(b):
    i = 0
    while i < len(b):
        k, i = varint(b, i)
        f, t = k >> 3, k & 7
        if t == 0:
            v, i = varint(b, i)
        elif t == 2:
            n, i = varint(b, i); v = b[i:i + n]; i += n
        elif t == 1:
            v = b[i:i + 8]; i += 8
        elif t == 5:
            v = b[i:i + 4]; i += 4
        else:
            raise ValueError(f'wire type {t}')
        yield f, v


if __name__ == '__main__':
    main()
