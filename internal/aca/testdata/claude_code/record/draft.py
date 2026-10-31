import sys,time
from rec import *
name,cols,rows=sys.argv[1],int(sys.argv[2]),int(sys.argv[3]); env=sys.argv[4] if len(sys.argv)>4 else ''
r=Rec(name,cols=cols,rows=rows,env=env)
def typ(t): r.tmux('send-keys','-t','r','-l',t)
def prompted(): return any('Esc to cancel' in x or x.strip()=='to cancel' for x in r.nb()[-3:])
def boxline():
    ls=r.nb()
    for i in range(len(ls)-1,0,-1):
        if ls[i].startswith('❯'): return ls[i][1:].strip()
    return ''
def wait_suggestion(limit=15):
    end=time.time()+limit
    while time.time()<end:
        if boxline(): return True
        time.sleep(.2)
    return False
def clear(): r.keys('C-c'); time.sleep(1.6)
r.trust(); time.sleep(3); r.snap('placeholder')
r.slow('hello there',.04); time.sleep(1); r.snap('typed_idle'); r.keys('C-u'); time.sleep(1); r.snap('cleared_idle')
typ('Try "create a util"'); time.sleep(1); r.snap('typed_dim_words'); r.keys('C-u'); time.sleep(1)
r.say("Say hi in one word."); r.settle(still=3,limit=30); got=wait_suggestion(); r.snap('suggestion' if got else 'no_suggestion_after_turn')
if got:
    typ('x'); time.sleep(1); r.snap('over_suggestion'); r.keys('BSpace'); time.sleep(1.5); r.snap('over_suggestion_erased')
    r.keys('Tab'); time.sleep(1); r.snap('tab_accepted'); r.keys('C-u'); time.sleep(1.5); r.snap('tab_cleared')
r.say("Say ok in one word."); r.settle(still=3,limit=30); got=wait_suggestion(); r.snap('suggestion2' if got else 'no_suggestion2')
if got: r.keys('Right'); time.sleep(1); r.snap('right_accepted'); r.keys('C-u'); time.sleep(1.5); r.snap('right_cleared')
for l in ['one\\','two\\']: typ(l); time.sleep(.1); r.keys('Enter'); time.sleep(.2)
typ('three'); time.sleep(1); r.snap('multi_line'); clear(); r.snap('multi_cleared')
for i in range(28): typ(f'line{i}\\'); time.sleep(.03); r.keys('Enter'); time.sleep(.05)
typ('last'); time.sleep(1); r.snap('long_draft'); clear(); r.snap('long_cleared')
typ('word '*70); time.sleep(1); r.snap('wrapped_draft'); clear()
r.tmux('set-buffer','-b','p','\n'.join(f'pasted {i}' for i in range(12))); r.tmux('paste-buffer','-p','-b','p','-t','r'); time.sleep(1.2); r.snap('pasted_draft'); clear(); r.snap('pasted_cleared')
typ('!'); time.sleep(1); r.snap('bash_alone'); typ('ls'); time.sleep(1); r.snap('bash_typed'); r.keys('C-u'); time.sleep(.5); r.keys('BSpace'); time.sleep(1.2); r.snap('bash_cleared'); r.keys('Escape'); time.sleep(.5)
typ('/he'); time.sleep(1.2); r.snap('slash_menu'); r.keys('C-u'); time.sleep(.6); r.keys('Escape'); time.sleep(1); r.snap('slash_cleared')
# A draft kept across a prompt.
r.say("Run with Bash in the foreground: ping -q -c 6 127.0.0.1",settle=.3); time.sleep(.3); r.slow('keepme',.03); r.snap('typed_turn_start')
end=time.time()+25
while time.time()<end and not prompted(): time.sleep(.1)
time.sleep(1); r.snap('prompt_over_draft'); r.keys('Enter'); time.sleep(2); r.snap('draft_back_in_turn'); r.settle(still=4,limit=25,approve=False); r.snap('draft_after_turn'); clear(); r.snap('draft_after_turn_cleared')
r.say("Run with Bash in the foreground: ping -q -c 7 127.0.0.2")
end=time.time()+25
while time.time()<end and not prompted(): time.sleep(.1)
r.keys('Enter'); time.sleep(1.5); typ('abc'); time.sleep(1); r.snap('typed_turn'); r.keys('C-u'); time.sleep(1); r.snap('cleared_turn'); r.settle(still=4,limit=25)
r.say("Bash with run_in_background true: ping -c 25 127.0.0.1 . Then end your turn with one word."); end=time.time()+25
while time.time()<end:
    if prompted(): r.keys('Enter'); time.sleep(1); continue
    if any(' shell' in x and 'mode on' in x and 'esc to' not in x for x in r.nb()[-3:]): break
    time.sleep(.2)
time.sleep(1); r.snap('shell_running'); typ('abc'); time.sleep(1); r.snap('typed_shell'); r.keys('C-u'); time.sleep(1); r.snap('cleared_shell'); r.settle(still=4,limit=30)
r.say("Agent with run_in_background true, subagent_type general-purpose, prompt: Run ping -c 12 127.0.0.1 with Bash, then say done. Then end your turn with one word."); end=time.time()+25
while time.time()<end:
    if prompted(): r.keys('Enter'); time.sleep(1); continue
    if any('◯' in x for x in r.nb()[-3:]): break
    time.sleep(.2)
time.sleep(1); r.snap('subagent_running'); typ('abc'); time.sleep(1); r.snap('typed_subagent'); r.keys('C-u'); time.sleep(1); r.snap('cleared_subagent'); r.settle(still=5,limit=30); r.snap('idle_end')
r.end()
