# Failure 3: The Silent Stoppage (Unanswered Prompts)
# An agent silently blocks on a confirmation prompt. The engineer thinks work is progressing.
# In wdx, VT screen parsing flags state as 'waiting', TIDE shows an amber row and '?' badge,
# and the prompt is answered in-place in seconds.

Output ../../public/blog/four-failures/unanswered-prompts.mp4
Source settings.tape

Hide
Type "export PS1='$ '; cd $DEMO/notes; clear"
Enter
Show

Type "wdx agent list"
Enter
Sleep 3s

Type "wdx tide"
Enter
Sleep 2s
Type "notes"
Sleep 400ms
Enter
Sleep 2s

# Move down in the project pane to notes-tests (amber '?' waiting badge)
Down
Sleep 800ms

# Open notes-tests tab with 'o' to inspect the blocked prompt
Type "o"
Sleep 2s

# Answer the prompt: select 'Yes, I trust this folder' and submit
Down
Sleep 700ms
Enter
Sleep 3s

# Quit TIDE: C-q q
Ctrl+q
Sleep 400ms
Type "q"
Sleep 1500ms

# Both agents are now actively working
Type "wdx agent list"
Enter
Sleep 3s
