import sys,time
from rec import *
name,cols,rows,mode=sys.argv[1],int(sys.argv[2]),int(sys.argv[3]),sys.argv[4]
r=Rec(name,cols=cols,rows=rows,env='NO_COLOR=1',args=f'--safe-mode --model haiku --permission-mode {mode}')
def typ(t): r.tmux('send-keys','-t','r','-l',t)
def prompted(): return any('Esc to cancel' in x or x.strip()=='to cancel' for x in r.nb()[-3:])
def boxline():
    ls=r.nb()
    for i in range(len(ls)-1,0,-1):
        if ls[i].startswith('❯'): return ls[i][1:].strip()
    return ''
def turn(t):
    r.say(t); r.settle(still=3,limit=30)
    end=time.time()+8
    while time.time()<end:
        if boxline(): return True
        time.sleep(.2)
    return False
r.trust(); time.sleep(3); r.snap('placeholder')
r.slow('hello',.05); time.sleep(1); r.snap('typed_idle'); r.keys('C-u'); time.sleep(1.5); r.snap('cleared_idle')
turn("Say hi in one word."); r.snap('after_turn1')
g=turn("Say ok in one word."); r.snap('suggestion' if g else 'no_suggestion')
if g:
    typ('x'); time.sleep(1); r.snap('over_suggestion'); r.keys('BSpace'); time.sleep(1.5); r.snap('over_suggestion_erased')
    r.keys('Tab'); time.sleep(1); r.snap('tab_accepted'); r.keys('C-u'); time.sleep(1.5); r.snap('tab_cleared')
r.say("Run with Bash in the foreground: ping -q -c 6 127.0.0.1")
end=time.time()+20
while time.time()<end and not prompted(): time.sleep(.1)
if prompted(): time.sleep(.5); r.snap('prompt'); r.keys('Enter')
time.sleep(1.5); r.snap('working_empty'); typ('abc'); time.sleep(1); r.snap('typed_turn'); r.keys('C-u'); time.sleep(1); r.settle(still=3,limit=25)
r.snap('idle_after_tool')
r.say("Agent with run_in_background true, subagent_type general-purpose, prompt: Run ping -c 5 127.0.0.1 with Bash, then say done. Then end your turn with one word."); end=time.time()+20
while time.time()<end:
    if prompted(): r.keys('Enter'); time.sleep(1); continue
    if any('◯' in x for x in r.nb()[-3:]): break
    time.sleep(.2)
time.sleep(.5); r.snap('subagent_running'); r.settle(still=4,limit=30); time.sleep(4); r.snap('tasks_footer')
typ('abc'); time.sleep(1); r.snap('typed_after_tasks'); r.keys('C-u'); time.sleep(1.5); r.snap('tasks_cleared')
r.end()
