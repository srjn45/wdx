"""Prints a capture-pane -e screen per line: plain, and with its dim cells blank (U), as a parser gets them."""
import re, sys
SGR = re.compile(r'\x1b\[([0-9;]*)m')
def undim(line):
    out, dim, pos = [], False, 0
    for m in SGR.finditer(line):
        txt = line[pos:m.start()]
        out.append(' ' * len(txt) if dim else txt)
        ps = [int(p) if p else 0 for p in m.group(1).split(';')] if m.group(1) else [0]
        i = 0
        while i < len(ps):
            p = ps[i]
            if p in (38, 48): i += 4 if ps[i+1:i+2] == [2] else 2; continue
            if p == 0 or p == 22: dim = False
            if p == 2: dim = True
            i += 1
        pos = m.end()
    txt = line[pos:]; out.append(' ' * len(txt) if dim else txt)
    return ''.join(out)
for f in sys.argv[1:]:
    print('==', f)
    lines = open(f).read().split('\n')
    while lines and not SGR.sub('', lines[-1]).strip(): lines.pop()
    for l in lines[-int(__import__('os').environ.get('N', '12')):]:
        plain = SGR.sub('', l).rstrip()
        u = undim(l).rstrip()
        print(f'  |{plain}' + ('' if u == plain else f'\n  U{u}'))
