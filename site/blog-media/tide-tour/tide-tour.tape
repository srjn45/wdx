# A tour of TIDE:
# 1. Shows parent coordinator and child count in CLI.
# 2. Launches wdx tide.
# 3. Navigates project tree: parent -> child agent -> worktree.
# 4. Opens worktree diff (o).
# 5. Leaves inline review comment (c).
# 6. Sends batch review to agent (s).
# 7. Agent addresses feedback; user asks agent to commit (C).
# 8. Lands worktree to main with one key (L -> y).
# 9. Removes worktree and child terminates (y).
# 10. Exits back to clean main.

Output ../../public/blog/tide-tour/tide-tour.mp4
Source settings.tape

Hide
Type "export PS1='$ '; cd $DEMO/notes; clear"
Enter
Show

Type "wdx agent list"
Enter
Sleep 2s

Type "wdx worktree list"
Enter
Sleep 2s

Type "wdx tide"
Enter
Sleep 2s

# Choose project notes if prompted
Type "notes"
Sleep 300ms
Enter
Sleep 2s

# Cursor starts at coordinator (row 0 of Agents).
# Move down through child agent to notes-count worktree:
Down
Sleep 800ms

Down
Sleep 800ms

Down
Sleep 800ms

# Open notes-count diff and switch focus to it ('o'):
Type "o"
Sleep 2s

# In diff view: navigate down into the implementation:
Down
Sleep 400ms
Down
Sleep 400ms
Down
Sleep 400ms
Down
Sleep 600ms

# Press 'c' to open inline comment dialog on this line:
Type "c"
Sleep 1s

# Type the review comment:
Type "Support singular '1 note' vs plural 'N notes'"
Sleep 800ms
Enter
Sleep 2s

# Press 's' to batch send review comments to the agent:
Type "s"
Sleep 1s
# Confirm sending review in the review box:
Enter
Sleep 2s

# Wait for review dispatch and agent's changes to appear:
Wait+Screen@30s /sent to count/
Wait+Screen@30s /C commit/
Sleep 2s

# Ask agent to commit uncommitted changes with C:
Type "C"
Sleep 1s
Enter
Sleep 2s

# Wait for agent to commit (diff updates to committed view with 'L land'):
Wait+Screen@30s /L land/
Sleep 2s

# Land worktree with L:
Type "L"
Sleep 1s
# Confirm landing with y:
Type "y"
Sleep 3s

# TIDE asks to remove the landed worktree:
Wait+Screen@15s /Remove/
Sleep 1s
Type "y"
Sleep 3s

# Quit TIDE: C-q q
Ctrl+q
Sleep 500ms
Type "q"
Sleep 1s

# Back in terminal, verify main git log and agent list:
Type "git log --oneline -n 2"
Enter
Sleep 2s

Type "wdx agent list"
Enter
Sleep 4s
