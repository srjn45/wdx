import sys,time
from rec import *
name=sys.argv[1]; env=sys.argv[2] if len(sys.argv)>2 else ''
r=Rec(name,args='--safe-mode --model haiku --permission-mode plan',env=env,cols=int(os.environ.get('RC','120')),rows=int(os.environ.get('RR','40')))
r.trust(); time.sleep(2); r.mark('idle'); time.sleep(2); r.snap('idle_plan_mode')
r.mark('turn_plan'); r.say("Plan: create hi.txt containing hi. No exploring. Call ExitPlanMode right away.")
time.sleep(2); r.snap('working_plan_mode')
ok=r.wait('Esc to cancel|Would you like|ready to code|Ready to code',60,where=12)
r.mark('plan_approval'); time.sleep(2); r.snap('plan_approval'); print(r.bottom(3),flush=True)
r.keys('Escape'); r.mark('after_esc'); time.sleep(3); r.snap('after_plan_esc'); r.turn_over(20); r.mark('idle_end'); time.sleep(2); r.snap('idle_end')
r.end()
