#!/bin/sh
phase=$(grep -m1 '(current)' ROADMAP.md | grep -oP 'Phase \K[0-9]+')
completed=$(awk "/## Phase.*\(current\)/{found=1; next} found && /- \[x\]/{count++} found && /## Phase/{exit} END{print count+0}" ROADMAP.md)
version="v${phase}.${completed}"
sed -i "s|version-v[0-9]\+\.[0-9]\+|version-${version}|" README.md
go build -o lash .
