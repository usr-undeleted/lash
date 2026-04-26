#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PREFS_FILE="$SCRIPT_DIR/.install_prefs"
CONFIG_DIR="$HOME/.config/lash"
THEMES_DIR="$CONFIG_DIR/themes"
REPO_THEMES="$SCRIPT_DIR/themes"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
DIM='\033[2m'
RESET='\033[0m'

USE_COLOR=1
if [ "${1:-}" = "--no-color" ]; then
    USE_COLOR=0
fi

c() {
    if [ "$USE_COLOR" = 1 ]; then
        printf '%b' "$1"
    else
        printf '%s' ""
    fi
}

info()    { c "$CYAN";  printf '  %s\n' "$*"; c "$RESET"; }
success() { c "$GREEN"; printf '  %s\n' "$*"; c "$RESET"; }
warn()    { c "$YELLOW"; printf '  %s\n' "$*"; c "$RESET"; }
error()   { c "$RED";   printf '  %s\n' "$*"; c "$RESET"; }
header()  { printf '\n'; c "$BOLD$CYAN"; printf '%s\n' "$*"; c "$RESET"; printf '\n'; }
bullet()  { c "$BOLD"; printf '  %s' "$1"; c "$RESET"; shift; printf ' %s\n' "$*"; }
desc()    { c "$DIM";   printf '      %s\n' "$*"; c "$RESET"; }

ask_choice() {
    local prompt="$1" default="$2"
    local has_default=0
    if [ -n "$default" ]; then
        has_default=1
    fi
    local input_src="/dev/tty"
    if [ ! -t 0 ]; then
        input_src="/dev/stdin"
    fi
    while true; do
        if [ "$has_default" = 1 ]; then
            c "$BOLD" >&2; printf '  %s' "$prompt" >&2; c "$DIM" >&2; printf ' [%s]' "$default" >&2; c "$RESET" >&2; printf ': ' >&2
        else
            c "$BOLD" >&2; printf '  %s' "$prompt" >&2; c "$RESET" >&2; printf ': ' >&2
        fi
        local answer=""
        read -r answer < "$input_src" || true
        if [ -z "$answer" ] && [ "$has_default" = 1 ]; then
            echo "$default"
            return 0
        fi
        if [ -n "$answer" ]; then
            echo "$answer"
            return 0
        fi
    done
}

ask_yn() {
    local prompt="$1" default_val="$2"
    local default_str="N"
    if [ "$default_val" = "1" ]; then
        default_str="Y"
    fi
    local input_src="/dev/tty"
    if [ ! -t 0 ]; then
        input_src="/dev/stdin"
    fi
    while true; do
        c "$BOLD" >&2; printf '  %s' "$prompt" >&2; c "$DIM" >&2; printf ' [y/N]' >&2; c "$RESET" >&2; printf '  ' >&2
        local answer=""
        read -r answer < "$input_src" || true
        case "$answer" in
            [Yy]|[Yy][Ee][Ss]) echo "1"; return 0 ;;
            [Nn]|[Nn][Oo]) echo "0"; return 0 ;;
            '') echo "$default_val"; return 0 ;;
            *) continue ;;
        esac
    done
}

ask_config() {
    local name="$1" desc="$2" default="$3"
    local val
    val=$(ask_yn "Enable $name? ($desc)" "$default")
    if [ "$val" = "$default" ]; then
        c "$DIM" >&2; printf '      kept default (%s)\n' "$default" >&2; c "$RESET" >&2
    fi
    echo "$val"
}

check_go() {
    if ! command -v go &>/dev/null; then
        error "Go is not installed. Install it from https://go.dev/dl/"
        exit 1
    fi
    local go_version
    go_version=$(go version | grep -oP 'go\K[0-9]+\.[0-9]+' || true)
    local major minor
    major="${go_version%%.*}"
    minor="${go_version#*.}"
    if [ "${major:-0}" -lt 1 ] || { [ "${major:-0}" -eq 1 ] && [ "${minor:-0}" -lt 22 ]; }; then
        error "Go >= 1.22 required, found ${go_version:-unknown}"
        exit 1
    fi
    success "Go $(go version | grep -oP 'go[0-9.]+') found"
}

build_lash() {
    header "Building lash"
    cd "$SCRIPT_DIR"
    bash build.sh
    if [ ! -f "$SCRIPT_DIR/lash" ]; then
        error "Build failed — binary not found"
        exit 1
    fi
    success "Build successful"
}

show_features() {
    header "Feature Overview"

    c "$BOLD"; printf '  Core\n'; c "$RESET"
    bullet 'Pipes & redirections'   '| > >> < << <(  >(  here-docs'
    bullet 'Job control'             'bg, fg, jobs, kill, Ctrl+Z suspension'
    bullet 'Command chaining'        '&& || ;'
    desc 'Full POSIX-compatible scripting with bash extensions.'

    printf '\n'
    c "$BOLD"; printf '  Scripting\n'; c "$RESET"
    bullet 'Control flow'             'if/elif/else, while, until, for, case, select'
    bullet 'Functions'                'with local variables, return values, arguments'
    bullet 'Arrays'                   'indexed & associative, slicing, iteration'
    bullet 'Subshells & grouping'     '(cmd) and { cmd; }'

    printf '\n'
    c "$BOLD"; printf '  Expansions\n'; c "$RESET"
    bullet 'Variable'                 '$VAR, ${VAR:-default}, ${VAR:offset:length}'
    bullet 'Command substitution'     '$(cmd) and backticks'
    bullet 'Arithmetic'               '$((expr))'
    bullet 'Process substitution'     '<(cmd) >(cmd)'
    bullet 'Brace expansion'          '{a,b,c} {1..10}'
    bullet 'Extended globbing'        '?(pat) *(pat) +(pat) @(pat) !(pat)'

    printf '\n'
    c "$BOLD"; printf '  Aliases (unique to lash)\n'; c "$RESET"
    desc 'Lash aliases use argument specifiers instead of simple text replacement:'
    bullet '{ALL}'                    'passes all arguments: alias ll {ALL} { ls -la $@ ; }'
    bullet '{NULL}'                   'no arguments: alias cls {NULL} { clear ; }'
    bullet '{1,2,...}'                'specific args: alias swap {2,1} { echo $2 $1 ; }'
    desc 'Run "lash help" after install for more details.'

    printf '\n'
    c "$BOLD"; printf '  Prompt\n'; c "$RESET"
    bullet 'Rich PS1'                 '\u \h \w \g (git) \x (exit status) \f/\F (fill)'
    bullet 'ANSI colors & octal'      'full color support in prompt'
    bullet 'Themes'                   'lash theme set <name> — 3 built-in themes'
    desc 'Run "lash theme list" to see available themes.'

    printf '\n'
    c "$BOLD"; printf '  Configuration\n'; c "$RESET"
    bullet 'lash set-config <key> <val>'   'all settings in one interface'
    bullet 'lash keybind ...'              'custom key bindings'
    bullet 'lash env ...'                  '.lashenv per-directory config with trust system'
    desc 'Config stored at ~/.config/lash/config'

    printf '\n'
    c "$BOLD"; printf '  Safety Options\n'; c "$RESET"
    bullet 'noclobber'                'refuse > on existing files (use >| to force)'
    bullet 'nounset'                  'error on unset variable expansion'
    bullet 'errexit'                  'exit immediately if a command fails'
    bullet 'pipefail'                 'pipeline fails if any command in it fails'

    printf '\n'
}

path_in_path() {
    local dir="$1"
    case ":${PATH}:" in
        *":${dir}:"*) return 0 ;;
    esac
    return 1
}

install_binary() {
    header "Install Location"

    local default_choice="1"
    local paths=("/usr/local/bin" "$HOME/.local/bin")
    local labels=("/usr/local/bin (recommended, requires sudo)" "$HOME/.local/bin (no sudo needed)")

    printf '  Where should lash be installed?\n\n'
    printf '    [1] %s\n' "${labels[0]}"
    printf '    [2] %s\n' "${labels[1]}"
    printf '    [3] Custom path\n\n'

    local choice
    choice=$(ask_choice "Choice" "$default_choice")

    local dest_dir=""
    case "$choice" in
        1) dest_dir="/usr/local/bin" ;;
        2) dest_dir="$HOME/.local/bin" ;;
        3)
            dest_dir=$(ask_choice "Enter install directory" "")
            if [ -z "$dest_dir" ]; then
                error "No path provided"
                exit 1
            fi
            ;;
        *) error "Invalid choice"; exit 1 ;;
    esac

    dest_dir="${dest_dir%/}"
    local dest="$dest_dir/lash"

    mkdir -p "$dest_dir" 2>/dev/null || {
        if [ "$choice" = "2" ]; then
            error "Cannot create $dest_dir"
            exit 1
        fi
        warn "Need sudo to create $dest_dir"
    }

    if [ -w "$dest_dir" ]; then
        cp "$SCRIPT_DIR/lash" "$dest"
        chmod +x "$dest"
    else
        sudo cp "$SCRIPT_DIR/lash" "$dest"
        sudo chmod +x "$dest"
    fi

    success "Installed to $dest"

    if ! path_in_path "$dest_dir"; then
        warn "$dest_dir is not in your PATH"
        c "$DIM"; desc "Add this to your shell profile to use lash from anywhere:"
        c "$BOLD"; desc "  export PATH=\"$dest_dir:\$PATH\""
        c "$RESET"
    fi

    echo "install_path=$dest" > "$PREFS_FILE.tmp"
}

remove_old_binary() {
    if [ -f "$PREFS_FILE" ]; then
        local old_path
        old_path=$(grep '^install_path=' "$PREFS_FILE" 2>/dev/null | cut -d= -f2- || true)
        if [ -n "$old_path" ] && [ -f "$old_path" ]; then
            if [ -w "$(dirname "$old_path")" ]; then
                rm -f "$old_path"
            else
                sudo rm -f "$old_path"
            fi
            info "Removed old binary at $old_path"
        fi
    fi
}

config_exists_and_valid() {
    local file="$1" type="$2"
    if [ ! -f "$file" ]; then
        return 1
    fi
    if [ ! -s "$file" ]; then
        return 1
    fi
    if [ "$type" = "rc" ]; then
        if grep -qv '^[[:space:]]*$\|^[[:space:]]*#' "$file" 2>/dev/null; then
            return 0
        fi
        return 1
    fi
    if [ "$type" = "config" ]; then
        if grep -qE '^[a-z].*=' "$file" 2>/dev/null; then
            return 0
        fi
        return 1
    fi
    return 0
}

ask_overwrite() {
    local file="$1"
    local val
    val=$(ask_yn "Overwrite $file?" "0")
    if [ "$val" = "1" ]; then
        return 0
    fi
    return 1
}

setup_rc_files() {
    header "RC & Profile Files"

    local created_rc=0
    local created_profile=0
    local any_config=0

    if config_exists_and_valid "$HOME/.lashrc" "rc"; then
        any_config=1
        if ask_overwrite "~/.lashrc"; then
            local editor="${EDITOR:-vi}"
            cat > "$HOME/.lashrc" << RCEOF
# lash startup configuration
# Lines starting with # are comments

# Environment variables
export EDITOR="$editor"
# export PATH="\$PATH:/custom/path"

# Source other rc files
# source ~/.lash_aliases

# Aliases
# alias ll {ALL} { ls -la \$@ ; }
RCEOF
            success "Overwrote ~/.lashrc"
            created_rc=1
        else
            info "~/.lashrc kept"
        fi
    elif [ -f "$HOME/.lashrc" ]; then
        warn "~/.lashrc exists but appears empty/invalid — overwriting"
        local editor="${EDITOR:-vi}"
        cat > "$HOME/.lashrc" << RCEOF
# lash startup configuration
# Lines starting with # are comments

# Environment variables
export EDITOR="$editor"
# export PATH="\$PATH:/custom/path"

# Source other rc files
# source ~/.lash_aliases

# Aliases
# alias ll {ALL} { ls -la \$@ ; }
RCEOF
        success "Created ~/.lashrc"
        created_rc=1
    else
        local editor="${EDITOR:-vi}"
        cat > "$HOME/.lashrc" << RCEOF
# lash startup configuration
# Lines starting with # are comments

# Environment variables
export EDITOR="$editor"
# export PATH="\$PATH:/custom/path"

# Source other rc files
# source ~/.lash_aliases

# Aliases
# alias ll {ALL} { ls -la \$@ ; }
RCEOF
        success "Created ~/.lashrc"
        created_rc=1
    fi

    if config_exists_and_valid "$HOME/.lash_profile" "rc"; then
        any_config=1
        if ask_overwrite "~/.lash_profile"; then
            cat > "$HOME/.lash_profile" << 'PROFEOF'
# lash login shell configuration
# Sourced only for login shells (lash login)
# Use this for environment setup that should run once

# export PATH="$HOME/bin:$PATH"
PROFEOF
            success "Overwrote ~/.lash_profile"
            created_profile=1
        else
            info "~/.lash_profile kept"
        fi
    elif [ -f "$HOME/.lash_profile" ]; then
        warn "~/.lash_profile exists but appears empty/invalid — overwriting"
        cat > "$HOME/.lash_profile" << 'PROFEOF'
# lash login shell configuration
# Sourced only for login shells (lash login)
# Use this for environment setup that should run once

# export PATH="$HOME/bin:$PATH"
PROFEOF
        success "Created ~/.lash_profile"
        created_profile=1
    else
        cat > "$HOME/.lash_profile" << 'PROFEOF'
# lash login shell configuration
# Sourced only for login shells (lash login)
# Use this for environment setup that should run once

# export PATH="$HOME/bin:$PATH"
PROFEOF
        success "Created ~/.lash_profile"
        created_profile=1
    fi

    echo "created_rc=$created_rc" >> "$PREFS_FILE.tmp"
    echo "created_profile=$created_profile" >> "$PREFS_FILE.tmp"
}

setup_config() {
    header "Configuration"

    if config_exists_and_valid "$CONFIG_DIR/config" "config"; then
        c "$DIM"; desc "Existing config detected."; c "$RESET"
        local val
        val=$(ask_yn "Reconfigure settings?" "0")
        if [ "$val" = "0" ]; then
            info "Keeping existing config"
            echo "noclobber=0" >> "$PREFS_FILE.tmp"
            echo "lashenv=0" >> "$PREFS_FILE.tmp"
            echo "ignoreeof=0" >> "$PREFS_FILE.tmp"
            echo "notify=0" >> "$PREFS_FILE.tmp"
            echo "hist-ignore-dups=0" >> "$PREFS_FILE.tmp"
            echo "hist-ignore-space=0" >> "$PREFS_FILE.tmp"
            echo "history-size=1000" >> "$PREFS_FILE.tmp"
            echo "colored-output=0" >> "$PREFS_FILE.tmp"
            echo "auto-suggest=0" >> "$PREFS_FILE.tmp"
            echo "auto-cd=0" >> "$PREFS_FILE.tmp"
            return 0
        fi
        printf '\n'
    fi

    c "$DIM"; desc "Answer each question, or press Enter to keep the default value."; c "$RESET"
    printf '\n'

    local noclobber lashenv auto_cd ignoreeof notify hist_dups hist_space colored_output

    noclobber=$(ask_config "noclobber" "prevent > overwriting existing files (use >| to force)" "0")
    lashenv=$(ask_config "lashenv" "auto-load per-directory .lashenv on cd" "0")
    auto_cd=$(ask_config "auto-cd" "change to directory when typed as a command" "0")
    ignoreeof=$(ask_config "ignoreeof" "require 10 Ctrl-D presses to exit" "0")
    notify=$(ask_config "notify" "report background job status immediately" "0")
    hist_dups=$(ask_config "hist-ignore-dups" "skip duplicate consecutive history entries" "0")
    hist_space=$(ask_config "hist-ignore-space" "skip commands starting with space from history" "0")
    colored_output=$(ask_config "colored-output" "set LS_COLORS and GREP_COLORS if not already defined" "1")
    auto_suggest=$(ask_config "auto-suggest" "show grayed-out inline completion hints as you type" "1")

    local hist_size=""
    while true; do
        hist_size=$(ask_choice "History size? (max number of history entries)" "1000")
        if [[ "$hist_size" =~ ^[0-9]+$ ]] && [ "$hist_size" -gt 0 ] 2>/dev/null; then
            break
        fi
        c "$YELLOW" >&2; printf '      must be a positive number\n' >&2; c "$RESET" >&2
    done

    echo "noclobber=$noclobber" >> "$PREFS_FILE.tmp"
    echo "lashenv=$lashenv" >> "$PREFS_FILE.tmp"
    echo "auto-cd=$auto_cd" >> "$PREFS_FILE.tmp"
    echo "ignoreeof=$ignoreeof" >> "$PREFS_FILE.tmp"
    echo "notify=$notify" >> "$PREFS_FILE.tmp"
    echo "hist-ignore-dups=$hist_dups" >> "$PREFS_FILE.tmp"
    echo "hist-ignore-space=$hist_space" >> "$PREFS_FILE.tmp"
    echo "history-size=$hist_size" >> "$PREFS_FILE.tmp"
    echo "colored-output=$colored_output" >> "$PREFS_FILE.tmp"
    echo "auto-suggest=$auto_suggest" >> "$PREFS_FILE.tmp"

    printf '\n'
    c "$DIM"; desc "Applying configuration..."; c "$RESET"

    mkdir -p "$CONFIG_DIR"

    if command -v "$SCRIPT_DIR/lash" &>/dev/null || [ -f "$SCRIPT_DIR/lash" ]; then
        "$SCRIPT_DIR/lash" set-config noclobber "$noclobber" 2>/dev/null || true
        "$SCRIPT_DIR/lash" set-config lashenv "$lashenv" 2>/dev/null || true
        "$SCRIPT_DIR/lash" set-config auto-cd "$auto_cd" 2>/dev/null || true
        "$SCRIPT_DIR/lash" set-config ignoreeof "$ignoreeof" 2>/dev/null || true
        "$SCRIPT_DIR/lash" set-config notify "$notify" 2>/dev/null || true
        "$SCRIPT_DIR/lash" set-config hist-ignore-dups "$hist_dups" 2>/dev/null || true
        "$SCRIPT_DIR/lash" set-config hist-ignore-space "$hist_space" 2>/dev/null || true
        "$SCRIPT_DIR/lash" set-config history-size "$hist_size" 2>/dev/null || true
        "$SCRIPT_DIR/lash" set-config colored-output "$colored_output" 2>/dev/null || true
        "$SCRIPT_DIR/lash" set-config auto-suggest "$auto_suggest" 2>/dev/null || true
        success "Configuration applied"
    else
        warn "Could not apply config — binary not found"
    fi
}

setup_themes() {
    header "Themes"

    mkdir -p "$THEMES_DIR"

    local theme_list=()
    if [ -d "$REPO_THEMES" ]; then
        for t in "$REPO_THEMES"/*; do
            [ -f "$t" ] || continue
            theme_list+=("$(basename "$t")")
        done
    fi

    if [ ${#theme_list[@]} -eq 0 ]; then
        info "No themes found in source"
        echo "theme=" >> "$PREFS_FILE.tmp"
        return 0
    fi

    for t in "${theme_list[@]}"; do
        cp "$REPO_THEMES/$t" "$THEMES_DIR/$t"
    done
    success "Copied ${#theme_list[@]} themes to $THEMES_DIR/"

    printf '  Defaults themes:\n\n'
    local i=1
    for t in "${theme_list[@]}"; do
        printf '    [%d] %s\n' "$i" "$t"
        ((i++))
    done
    printf '    [%d] None (keep default prompt)\n' "$i"
    printf '\n'

    local choice
    choice=$(ask_choice "Select a theme" "$i")

    if [ "$choice" = "$i" ] || [ "$choice" = "" ]; then
        info "No theme selected"
        echo "theme=" >> "$PREFS_FILE.tmp"
        return 0
    fi

    if ! [[ "$choice" =~ ^[0-9]+$ ]] || [ "$choice" -lt 1 ] || [ "$choice" -gt ${#theme_list[@]} ]; then
        warn "Invalid choice, skipping theme"
        echo "theme=" >> "$PREFS_FILE.tmp"
        return 0
    fi

    local selected="${theme_list[$((choice-1))]}"

    if [ -f "$SCRIPT_DIR/lash" ]; then
        "$SCRIPT_DIR/lash" theme set "$selected" 2>/dev/null || true
    fi

    echo "theme=$selected" >> "$PREFS_FILE.tmp"
}

write_prefs() {
    {
        echo "# dont edit file, used by install script."
        cat "$PREFS_FILE.tmp"
    } > "$PREFS_FILE"
    rm -f "$PREFS_FILE.tmp"
}

show_done() {
    header "Installation Complete"

    local install_path=""
    local theme=""
    if [ -f "$PREFS_FILE" ]; then
        install_path=$(grep '^install_path=' "$PREFS_FILE" | cut -d= -f2- || true)
        theme=$(grep '^theme=' "$PREFS_FILE" | cut -d= -f2- || true)
    fi

    printf '  %s\n' "Binary:  ${install_path:-unknown}"
    printf '  %s\n' "Config:  $CONFIG_DIR/config"
    printf '  %s\n' "RC:      ~/.lashrc"
    printf '  %s\n' "Profile: ~/.lash_profile"
    if [ -n "$theme" ]; then
        printf '  %s\n' "Theme:   $theme"
    fi
    printf '\n'

    c "$BOLD"; printf '  To start lash:\n'; c "$RESET"
    bullet 'lash'          'start an interactive session'
    bullet 'lash login'    'start as a login shell'
    bullet 'lash help'     'show all available commands'
    bullet 'lash set-config list'  'see all configuration options'
    printf '\n'
}

uninstall() {
    header "Uninstall lash"

    local install_path=""
    if [ -f "$PREFS_FILE" ]; then
        install_path=$(grep '^install_path=' "$PREFS_FILE" | cut -d= -f2- || true)
    fi

    if [ -z "$install_path" ] || [ ! -f "$install_path" ]; then
        warn "No installed binary found"
    else
        if [ -w "$(dirname "$install_path")" ]; then
            rm -f "$install_path"
        else
            sudo rm -f "$install_path"
        fi
        success "Removed binary at $install_path"
    fi

    c "$BOLD"; printf '  Remove configuration files?\n'; c "$RESET"
    printf '    [1] Remove config only (~/.config/lash/config)\n'
    printf '    [2] Remove config + themes (~/.config/lash/)\n'
    printf '    [3] Remove config + themes + rc files (~/.lashrc, ~/.lash_profile)\n'
    printf '    [4] Keep all config files\n\n'

    local choice
    choice=$(ask_choice "Choice" "4")

    case "$choice" in
        1)
            rm -f "$CONFIG_DIR/config"
            success "Removed config"
            ;;
        2)
            rm -rf "$CONFIG_DIR"
            success "Removed ~/.config/lash/"
            ;;
        3)
            rm -rf "$CONFIG_DIR"
            rm -f "$HOME/.lashrc"
            rm -f "$HOME/.lash_profile"
            success "Removed config, themes, rc, and profile files"
            ;;
        4)
            info "Config files kept"
            ;;
        *)
            error "Invalid choice"
            exit 1
            ;;
    esac

    rm -f "$PREFS_FILE"
    success "Uninstall complete"
}

reinstall() {
    local old_path
    old_path=$(grep '^install_path=' "$PREFS_FILE" | cut -d= -f2- || true)

    header "Previous Installation Detected"
    info "Last install path: ${old_path:-unknown}"
    printf '\n'
    printf '  [1] Re-install (build + copy binary, keep config)\n'
    printf '  [2] Full setup (remove old binary, reconfigure everything)\n'
    printf '  [3] Uninstall\n'
    printf '  [4] Cancel\n\n'

    local choice
    choice=$(ask_choice "Choice" "1")

    case "$choice" in
        1)
            check_go
            build_lash
            local dest_dir
            dest_dir="$(dirname "$old_path")"
            mkdir -p "$dest_dir" 2>/dev/null || true
            if [ -w "$dest_dir" ]; then
                cp "$SCRIPT_DIR/lash" "$old_path"
                chmod +x "$old_path"
            else
                sudo cp "$SCRIPT_DIR/lash" "$old_path"
                sudo chmod +x "$old_path"
            fi
            success "Re-installed to $old_path"
            ;;
        2)
            remove_old_binary
            check_go
            build_lash
            show_features
            rm -f "$PREFS_FILE.tmp"
            install_binary
            setup_rc_files
            setup_config
            setup_themes
            write_prefs
            show_done
            ;;
        3)
            uninstall
            exit 0
            ;;
        4)
            info "Cancelled"
            exit 0
            ;;
        *)
            error "Invalid choice"
            exit 1
            ;;
    esac
}

full_setup() {
    check_go
    build_lash

    c "$BOLD"; c "$CYAN"; printf '\n  ╭──────────────────────────────────────╮\n'
    printf '  │          Welcome to lash             │\n'
    printf '  │   A Linux shell written in Go        │\n'
    printf '  ╰──────────────────────────────────────╯\n'
    c "$RESET"
    printf '\n'

    show_features

    rm -f "$PREFS_FILE.tmp"
    install_binary
    setup_rc_files
    setup_config
    setup_themes
    write_prefs
    show_done
}

if [ -f "$PREFS_FILE" ]; then
    reinstall
else
    full_setup
fi
