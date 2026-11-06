"""Writes frames.log: every bottom of the screen that changed, to check the marks against."""
import threading, time
def start_frames(r, n=14):
    stop = threading.Event()
    def run():
        prev = None
        with open(f'{r.out}/frames.log', 'w') as f:
            while not stop.is_set():
                ls = [l for l in r.lines() if l.strip()][-n:]
                b = '\n'.join(ls)
                if b != prev:
                    f.write(f'--- {time.time()-r.t0:7.2f}\n{b}\n'); f.flush(); prev = b
                time.sleep(0.05)
    threading.Thread(target=run, daemon=True).start()
    return stop
