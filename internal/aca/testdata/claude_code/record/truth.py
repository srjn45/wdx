import json,os,statistics
from datetime import datetime
OUT=os.path.dirname(os.path.abspath(__file__))+'/out'
MAP={'n80':'motion-80x24','n60':'motion-60x24','n40':'motion-40x24','cn80':'classic-motion-80x24','cn40':'classic-motion-40x24','rm80':'motion-reduced-80x24','n120':'motion-120x40','m80':'idle-typing-80x24','m40':'idle-typing-40x24','cm40':'classic-idle-typing-40x24'}
def inputs(d):
    at=0;res=[]
    for l in open(f'{d}/raw.timing'):
        f=l.split()
        if f[0] in('H','S'): continue
        at+=float(f[1])
        if f[0]=='I': res.append((at,int(f[2])))
    return res
def ts(r): return datetime.fromisoformat(r['timestamp'].replace('Z','+00:00')).timestamp()
res={}
for k,name in MAP.items():
    d=f'{OUT}/{k}'; rows=[json.loads(l) for l in open(d+'/transcript.jsonl')]
    ins=inputs(d)
    enters=[a for a,n in ins if n==1]
    typed=[ts(r) for r in rows if r.get('type')=='user' and isinstance(r['message'].get('content'),str) and r['message']['content'].startswith(('Agent with','Write a','Run with'))]
    best=None
    for e in enters:
        o=e-typed[0]
        cost=sorted(min(abs(t+o-x) for x in enters) for t in typed)
        c=sum(cost[:-1])
        if best is None or c<best[0]: best=(c,o)
    off=best[1]
    resid=[round(min(abs(t+off-x) for x in enters)*1000) for t in typed]
    off+=statistics.median([min((t+off-x for x in enters),key=abs)*-1 for t in typed])
    turns=[];cur=None
    for r in rows:
        if 'timestamp' not in r: continue
        t=ts(r)+off
        if r.get('type')=='api-request' and cur is None: cur=t
        if r.get('type')=='system' and r.get('subtype')=='turn_duration' and cur is not None:
            turns.append((round(cur,3),round(t,3))); cur=None
    # Enter is the turn's real start, for a typed prompt.
    fixed=[]
    for a,b in turns:
        e=[x for x in enters if -0.05<=a-x<0.45]
        fixed.append((round(e[0],3) if e else a,b))
    res[name]=fixed
    print(name,'prompts',len(typed),'residual ms',resid); print('   ',fixed)
json.dump(res,open(OUT+'/truth.json','w'))
