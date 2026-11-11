# A Claude Code agent, lead, splits a change into two child agents, each in a
# worktree of its own, all seen in TIDE; then wdx agent list shows the tree.
# Run by record.sh, inside the demo it makes.
Output ../../public/blog/wdx-builds-wdx/children.mp4
Source settings.tape

Hide
Type "export PS1='$ '; cd $DEMO/notes; clear"
Enter
Show

Type `wdx agent create --name lead --prompt "Add 'delete <n>' and 'search <text>' to notes.py: one child agent per command, each in a worktree of its own"`
Sleep 500ms
Enter
Sleep 2s

# A new folder: Claude Code asks whether to trust it. Answer it in the agent's
# own screen, then detach with C-\ d.
Type "wdx agent attach --name lead"
Enter
Wait+Screen@60s /trust this folder/
Sleep 2s
Down
Sleep 700ms
Enter
Sleep 3s
Ctrl+\
Type "d"
Sleep 1s

Type "wdx tide"
Enter
Sleep 2s
Type "notes"
Sleep 400ms
Enter
Wait+Screen@240s /notes-delete\s+↑1/
Wait+Screen@240s /notes-search\s+↑1/
Sleep 3s
Down
Sleep 500ms
Enter
Sleep 5s
Down
Sleep 500ms
Enter
Sleep 5s

Ctrl+q
Sleep 800ms
Type "q"
Sleep 1s
Type "wdx agent list"
Enter
Sleep 1s
Type "wdx worktree list"
Enter
Sleep 5s
