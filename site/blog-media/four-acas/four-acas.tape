# Four AI coding agents under one tool:
# 1. Inspect installed and enabled ACAs (wdx agent acas).
# 2. Inspect account rate limits and reset windows across providers (wdx usage).
# 3. List running agents across all four ACAs with their models, states, contexts (wdx agent list).
# 4. Open wdx tide to navigate the multi-ACA fleet in one unified terminal IDE.

Output ../../public/blog/four-acas/four-acas.mp4
Source settings.tape

Hide
Type "export PS1='$ '; cd $DEMO/notes; clear"
Enter
Show

Type "wdx agent acas"
Sleep 400ms
Enter
Sleep 2s

Type "wdx usage"
Sleep 400ms
Enter
Sleep 3s

Type "wdx agent list"
Sleep 400ms
Enter
Sleep 3s

Type "wdx tide"
Sleep 400ms
Enter
Sleep 2s

# Select project if prompted
Type "notes"
Sleep 300ms
Enter
Sleep 2s

# Move through the agents in TIDE's project pane
Down
Sleep 800ms
Down
Sleep 800ms
Down
Sleep 800ms
Enter
Sleep 2s

# Open contextual menu (Ctrl+q)
Ctrl+q
Sleep 2s

# Close menu and exit TIDE
Escape
Sleep 500ms
Ctrl+q
Sleep 400ms
Type "q"
Sleep 1s

Type "wdx worktree list"
Sleep 400ms
Enter
Sleep 3s
