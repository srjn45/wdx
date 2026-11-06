"""transcript.py <name>: one conversation's files, as BackUp would copy them.

A turn running a command, one writing then editing a file, one reading it, /compact,
/exit, a resume with --conversation for one more turn, then /clear and a turn in the
new conversation. Keeps the hooks log and each conversation's brain logs.
"""
import json, os, shutil, sys, time
from rec import Rec
G = os.path.expanduser('~/.gemini/antigravity-cli')
name = sys.argv[1]
r = Rec(name, 120, 40)
def turn(text, done, limit=90):
    r.send(text); r.wait(done, limit); time.sleep(2)
r.wait(r'Flash'); time.sleep(1)
turn('Run the shell command `echo one; ls`, then reply DONE1.', r'^  DONE1')
turn('Create the file notes.txt containing the line "alpha", then change alpha to beta with an edit, then reply DONE2.', r'^  DONE2')
turn('Read notes.txt with your file viewing tool and reply with its content only.', r'^  beta')
r.type('/compact'); time.sleep(0.6); r.keys('Enter'); r.wait(r'ompact', 30); r.still(4, 120); r.snap('compacted')
r.keys('C-u'); r.type('/exit'); time.sleep(0.5); r.keys('Enter'); time.sleep(3)
first = [json.loads(l.split(' ', 1)[1]) for l in open(r.hooklog)][0]['conversationId']
r.kill()
r.start('--conversation=' + first, raw='raw2')
r.wait(r'Flash'); time.sleep(1.5)
turn('What file did you create earlier? Reply with its name only.', r'notes\.txt$')
r.keys('C-u'); r.type('/clear'); time.sleep(0.5); r.keys('Enter'); time.sleep(2)
turn('Reply VICTOR.', r'^  VICTOR')
r.keys('C-u'); r.type('/exit'); time.sleep(0.5); r.keys('Enter'); time.sleep(3)
r.kill()
ids = []
for l in open(r.hooklog):
    i = json.loads(l.split(' ', 1)[1])['conversationId']
    if i not in ids: ids.append(i)
for i in ids:
    dst = os.path.join(r.out, 'brain', i)
    shutil.copytree(os.path.join(G, 'brain', i, '.system_generated'), dst, ignore=shutil.ignore_patterns('chunks'))
shutil.copy(r.hooklog, os.path.join(r.out, 'hooks.jsonl'))
print('conversations', ids, 'work', r.work)
