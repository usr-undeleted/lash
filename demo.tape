# lash demo tape
# Renders with: vhs demo.tape -o logo/demo.gif

Set FontFamily "JetBrains Mono"
Set FontSize 14
Set Width 800
Set Height 500
Set PlaybackSpeed 0.85

# Set up environment
Hide
Type "mkdir -p /tmp/lash-demo && cd /tmp/lash-demo && mkdir -p projects documents music"
Enter
Sleep 500ms
Type "cd ~/projects/lash"
Enter
Sleep 300ms
Show

# Startup — launch lash
Type "./lash"
Enter
Sleep 800ms

# ── Auto-correct: builtin typo ──
Type "# auto-correct: fixes mistyped commands"
Enter
Sleep 500ms
Type "ehco hello world"
Enter
Sleep 500ms
Type "lsh version"
Enter
Sleep 800ms

# ── Auto-cd / Tab completion ──
Type "# auto-cd: type directory to cd"
Enter
Sleep 500ms
Type "cd /tmp/lash-demo"
Enter
Sleep 400ms

# ── Tab cycling demo ──
Type "# tab cycling through completions"
Enter
Sleep 500ms
Type "doc"
Sleep 300ms
Tab
Sleep 400ms
Tab
Sleep 400ms
Tab
Sleep 300ms
Enter
Sleep 400ms

# ── Fuzzy tab completion ──
Type "# fuzzy tab completion on typos"
Enter
Sleep 500ms
Type "prj"
Sleep 200ms
Tab
Sleep 500ms
Escape
Ctrl+U
Sleep 300ms

# ── cd-only directory completion ──
Type "# cd shows only directories"
Enter
Sleep 500ms
Type "cd "
Tab
Sleep 500ms
Tab
Sleep 400ms
Tab
Sleep 300ms
Enter
Sleep 400ms

# ── Built-in fetch ──
Type "# built-in fetch command"
Enter
Sleep 400ms
Type "fetch"
Enter
Sleep 1000ms

# ── lash doctor ──
Type "# lash doctor: system diagnostics"
Enter
Sleep 400ms
Type "lash doctor"
Enter
Sleep 600ms

# ── lash update ──
Type "# self-update"
Enter
Sleep 400ms
Type "lash update"
Enter
Sleep 600ms

# ── Theme switching ──
Type "# themes"
Enter
Sleep 400ms
Type "lash theme list"
Enter
Sleep 500ms
Type "lash theme set arrow"
Enter
Sleep 300ms
Type "echo new theme applied!"
Enter
Sleep 500ms
Type "lash theme set slim"
Enter
Sleep 300ms

# ── Syntax highlighting visual ──
Type "# syntax highlighting: green = valid, red = invalid"
Enter
Sleep 500ms
Type "echo this is green"
Sleep 800ms
Ctrl+U
Sleep 300ms
Type "thisshouldbered"
Sleep 800ms
Ctrl+U
Sleep 300ms

# Done
Type "exit"
Enter
Sleep 300ms
