Set FontFamily "JetBrains Mono"
Set FontSize 14
Set Width 800
Set Height 500
Set PlaybackSpeed 0.6

Hide
Type "mkdir -p /tmp/lash-demo && cd /tmp/lash-demo && mkdir -p projects documents music"
Enter
Sleep 500ms
Type "cd ~/projects/lash"
Enter
Sleep 300ms
Show

Type "./lash"
Enter
Sleep 1s

Type "ehco hello world"
Enter
Sleep 800ms

Type "lsh version"
Enter
Sleep 1s

Type "cd /tmp/lash-demo"
Enter
Sleep 600ms

Type "doc"
Sleep 400ms
Tab
Sleep 500ms
Tab
Sleep 500ms
Tab
Sleep 400ms
Enter
Sleep 600ms

Type "prj"
Sleep 300ms
Tab
Sleep 600ms
Escape
Ctrl+U
Sleep 400ms

Type "cd "
Tab
Sleep 500ms
Tab
Sleep 400ms
Tab
Sleep 400ms
Escape
Ctrl+U
Sleep 400ms

Type "fetch"
Enter
Sleep 1.5s

Type "lash doctor"
Enter
Sleep 1s

Type "lash theme list"
Enter
Sleep 800ms
Type "lash theme set arrow"
Enter
Sleep 500ms
Type "echo new theme"
Enter
Sleep 800ms
Type "lash theme set slim"
Enter
Sleep 500ms

Type "echo this is green"
Sleep 1s
Ctrl+U
Sleep 400ms
Type "thisshouldbered"
Sleep 1s
Ctrl+U
Sleep 400ms

Type "exit"
Enter
Sleep 300ms
