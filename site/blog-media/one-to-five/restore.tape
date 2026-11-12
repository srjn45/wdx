# Stand-in reboot: the daemon and terminals stop, simulating a restart.
# Then the daemon starts, the agent shows lost, and wdx agent start brings
# it back in the same session with all context intact.
Output ../../public/blog/one-to-five/restore.mp4
Source settings.tape

Hide
Type "export PS1='$ '; cd $DEMO/notes; clear"
Enter
Show

Type "wdx agent list"
Enter
Sleep 2s

# Stand-in reboot: stop daemon and terminate holders
Type "$DEMO/reboot.sh"
Enter
Sleep 2s

Type "wdx daemon start"
Enter
Sleep 2s

Type "wdx agent list"
Enter
Sleep 4s

Type "wdx agent start --name notes-cli"
Enter
Sleep 2s

Type "wdx agent list"
Enter
Sleep 4s
