import sys,time
from rec import *
name,cols,rows=sys.argv[1],int(sys.argv[2]),int(sys.argv[3]); env=sys.argv[4] if len(sys.argv)>4 else ''
r=Rec(name,cols=cols,rows=rows,env=env)
def typ(t): r.tmux('send-keys','-t','r','-l',t)
def boxline():
    ls=r.nb()
    for i in range(len(ls)-1,0,-1):
        if ls[i].startswith('❯'): return ls[i][1:].strip()
    return ''
def turn(t):
    for _ in range(3):
        r.say(t); r.settle(still=3,limit=30)
        end=time.time()+8
        while time.time()<end:
            if boxline(): return True
            time.sleep(.2)
    return False
r.trust(); time.sleep(2)
typ('1. buy milk'); time.sleep(1); r.snap('numbered_draft'); r.keys('C-u'); time.sleep(1)
typ('❯ quoted'); time.sleep(1); r.snap('draft_with_prompt_mark'); r.keys('C-u'); time.sleep(1)
g=turn("Say hi in one word.")
g=turn("Say ok in one word.") or g
if g:
    r.snap('suggestion'); typ('x'); time.sleep(1); r.snap('over_suggestion'); r.keys('BSpace'); time.sleep(1.5); r.snap('over_suggestion_erased')
    r.keys('Tab'); time.sleep(1); r.snap('tab_accepted'); typ(' more'); time.sleep(1); r.snap('tab_accepted_extended'); r.keys('C-u'); time.sleep(1.5); r.snap('tab_cleared')
g=turn("Say yes in one word.")
if g: r.snap('suggestion2'); r.keys('Right'); time.sleep(1); r.snap('right_accepted'); r.keys('C-u'); time.sleep(1.5); r.snap('right_cleared')
r.snap('idle_end'); r.end()
