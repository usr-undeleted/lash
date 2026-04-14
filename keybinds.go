package main

import (
	"sort"
	"strings"
)

const (
	actBeginningOfLine  = "beginning_of_line"
	actEndOfLine        = "end_of_line"
	actKillLineStart    = "kill_line_start"
	actKillLineEnd      = "kill_line_end"
	actDeleteWordBack   = "delete_word_back"
	actDeleteWordWSBack = "delete_whitespace_word_back"
	actDeleteChar       = "delete_char"
	actBackspace        = "backspace"
	actClearScreen      = "clear_screen"
	actReverseSearch    = "reverse_search"
	actAcceptLine       = "accept_line"
	actEOF              = "eof"
	actInterrupt        = "interrupt"
	actSuspend          = "suspend"
	actWordBack         = "word_back"
	actWordForward      = "word_forward"
	actHistoryBack      = "history_back"
	actHistoryForward   = "history_forward"
	actComplete         = "complete"
	actCursorLeft       = "cursor_left"
	actCursorRight      = "cursor_right"
	actNop              = "nop"
)

var allActions = []string{
	actBeginningOfLine, actEndOfLine, actKillLineStart, actKillLineEnd,
	actDeleteWordBack, actDeleteWordWSBack, actDeleteChar, actBackspace,
	actClearScreen, actReverseSearch, actAcceptLine, actEOF, actInterrupt,
	actSuspend, actWordBack, actWordForward, actHistoryBack, actHistoryForward,
	actComplete, actCursorLeft, actCursorRight, actNop,
}

var defaultKeybinds = map[string]string{
	"ctrl-a":    actBeginningOfLine,
	"ctrl-b":    actCursorLeft,
	"ctrl-c":    actInterrupt,
	"ctrl-d":    actEOF,
	"ctrl-e":    actEndOfLine,
	"ctrl-f":    actCursorRight,
	"ctrl-h":    actBackspace,
	"ctrl-i":    actComplete,
	"ctrl-j":    actAcceptLine,
	"ctrl-k":    actKillLineEnd,
	"ctrl-l":    actClearScreen,
	"ctrl-m":    actAcceptLine,
	"ctrl-n":    actHistoryForward,
	"ctrl-p":    actHistoryBack,
	"ctrl-r":    actReverseSearch,
	"ctrl-u":    actKillLineStart,
	"ctrl-w":    actDeleteWordBack,
	"ctrl-z":    actSuspend,
	"up":        actHistoryBack,
	"down":      actHistoryForward,
	"right":     actCursorRight,
	"left":      actCursorLeft,
	"home":      actBeginningOfLine,
	"end":       actEndOfLine,
	"delete":    actDeleteChar,
	"backspace": actBackspace,
}

func keyNameToSequence(name string) string {
	name = strings.ToLower(name)

	if strings.HasPrefix(name, "ctrl-") && len(name) == 6 {
		c := name[5]
		if c >= 'a' && c <= 'z' {
			return string(rune(c - 'a' + 1))
		}
		switch c {
		case ' ':
			return "\x00"
		case '[':
			return "\x1b"
		case '\\':
			return "\x1c"
		case ']':
			return "\x1d"
		case '^':
			return "\x1e"
		case '_':
			return "\x1f"
		}
	}

	if strings.HasPrefix(name, "alt-") && len(name) == 5 {
		c := name[4]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			return "\x1b" + string(c)
		}
	}

	if strings.HasPrefix(name, "ctrl-") && len(name) > 6 {
		rest := name[5:]
		switch rest {
		case "up":
			return "\x1b[1;5A"
		case "down":
			return "\x1b[1;5B"
		case "right":
			return "\x1b[1;5C"
		case "left":
			return "\x1b[1;5D"
		case "home":
			return "\x1b[1;5H"
		case "end":
			return "\x1b[1;5F"
		case "delete":
			return "\x1b[3;5~"
		case "backspace":
			return "\x1b\x7f"
		}
	}

	switch name {
	case "up":
		return "\x1b[A"
	case "down":
		return "\x1b[B"
	case "right":
		return "\x1b[C"
	case "left":
		return "\x1b[D"
	case "home":
		return "\x1b[H"
	case "end":
		return "\x1b[F"
	case "delete":
		return "\x1b[3~"
	case "insert":
		return "\x1b[2~"
	case "backspace":
		return "\x7f"
	case "pageup":
		return "\x1b[5~"
	case "pagedown":
		return "\x1b[6~"
	case "f1":
		return "\x1bOP"
	case "f2":
		return "\x1bOQ"
	case "f3":
		return "\x1bOR"
	case "f4":
		return "\x1bOS"
	case "f5":
		return "\x1b[15~"
	case "f6":
		return "\x1b[17~"
	case "f7":
		return "\x1b[18~"
	case "f8":
		return "\x1b[19~"
	case "f9":
		return "\x1b[20~"
	case "f10":
		return "\x1b[21~"
	case "f11":
		return "\x1b[23~"
	case "f12":
		return "\x1b[24~"
	case "enter", "return":
		return "\r"
	case "space":
		return " "
	case "tab":
		return "\t"
	case "escape", "esc":
		return "\x1b"
	}

	return ""
}

func isValidKey(name string) bool {
	return keyNameToSequence(name) != ""
}

func isValidAction(name string) bool {
	for _, a := range allActions {
		if a == name {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
