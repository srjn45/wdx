# Failure 1: The Disappearing Session (Lost Terminals)
# An agent in a plain terminal dies when the tab closes or the machine restarts.
# In wdx, agents run in detached daemon holders; after a simulated reboot,
# wdx tracks the session as 'lost' and resumes it in place with all context intact.

Output ../../public/blog/four-failures/lost-terminals.mp4
Source settings.tape

Hide
Type "export PS1='$ '; cd $DEMO/notes; clear"
Enter
Show

Type "wdx agent list"
Enter
Sleep 2s

# Attach to the running agent, then detach safely (C-\ d)
Type "wdx agent attach --name notes-worker"
Enter
Sleep 2s
Ctrl+\
Type "d"
Sleep 1500ms

# Stand-in reboot: stop daemon and terminate holders
Type "$DEMO/reboot.sh"
Enter
Sleep 2s

# Daemon starts back up after reboot
Type "wdx daemon start"
Enter
Sleep 2s

# Agent session is tracked as lost across reboots
Type "wdx agent list"
Enter
Sleep 3s

# Resume the agent in the same session with all context preserved
Type "wdx agent start --name notes-worker"
Enter
Sleep 2s

Type "wdx agent list"
Enter
Sleep 3s
