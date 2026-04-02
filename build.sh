#!/bin/sh
version=$(awk '
/## Phase / {
    match($0, /Phase ([0-9]+)/, m)
    n = ++nphases
    phase_num[n] = m[1]
    phase_done[n] = 0
    phase_total[n] = 0
    if (/\(current\)/) current_idx = n
    next
}
/- \[x\]/ { phase_done[nphases]++; phase_total[nphases]++ }
/- \[ \]/ { phase_total[nphases]++ }
END {
    idx = current_idx
    while (idx <= nphases && phase_total[idx] > 0 && phase_done[idx] == phase_total[idx]) {
        idx++
    }
    if (idx > nphases) idx = nphases
    print "v" phase_num[idx] "." phase_done[idx]
}
' ROADMAP.md)
current_patch=$(grep -oP 'version-v[0-9]+\.[0-9]+\.\K[0-9]+' README.md || true)
current_mm=$(grep -oP 'version-v\K[0-9]+\.[0-9]+' README.md)
new_mm=$(echo "$version" | sed 's/^v//')
if [ "$current_mm" = "$new_mm" ] && [ -n "$current_patch" ]; then
    sed -i "s|version-v[0-9]\+\.[0-9]\+\.[0-9]\+|version-${version}.${current_patch}|" README.md
else
    sed -i "s|version-v[0-9]\+\.[0-9]\+\(\.[0-9]\+\)\?|version-${version}|" README.md
fi
go build -o lash .
[ -f ~/.lashrc ] || cat > ~/.lashrc << 'EOF'
# lash startup configuration
# Lines starting with # are comments

# Environment variables
# export EDITOR="vim"
# export PATH="$PATH:/custom/path"

# Source other rc files
# source ~/.lash_aliases

# Aliases
# alias ll {ALL} { ls -la $@ ; }
EOF
mkdir -p ~/.config/lash
[ -f ~/.config/lash/config ] || cat > ~/.config/lash/config << 'EOF'
# lash configuration
# key = value

syntax-color = 0
logosize = big
history-size = 1000
glob-dotfiles = 0
glob-case-sensitivity = 1
EOF
