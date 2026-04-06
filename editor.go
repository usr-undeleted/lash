package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/term"
)

type LineEditor struct {
	buf       []rune
	cursor    int
	history   []string
	histIdx   int
	config    *Config
	reader    *bufio.Reader
	accepted  bool
	keySeqs   map[string]string // terminfo extended key sequences: sequence -> action name
	screenRow int
}

// action constants for key sequence dispatch
const (
	actionDeleteWordBack = "delete_word_back"
)

func NewLineEditor(cfg *Config) *LineEditor {
	e := &LineEditor{config: cfg}
	e.initKeySequences()
	e.loadHistory()
	return e
}

// initKeySequences queries terminfo for extended key capabilities
// so that Ctrl+Backspace and other modified keys work across terminals.
func (e *LineEditor) initKeySequences() {
	e.keySeqs = make(map[string]string)

	// Try terminfo capabilities for extended keys.
	// The kEXT capability (extended key) maps function keys, but
	// modified keys like Ctrl+Backspace are terminal-specific.
	//
	// Common sequences by terminal:
	//   kitty/foot/wezterm: \x1b\x7f (ESC DEL) for Ctrl+Backspace
	//   xterm:              \x1b[3;5~ (CSI 3 ; 5 ~)
	//   tmux:               \x1b\x7f
	//   rxvt:               \x1b[3^
	//
	// We register all known sequences so it works everywhere without
	// requiring users to configure their terminal.

	// kitty / foot / wezterm / tmux / alacritty (with custom binding)
	e.keySeqs["\x1b\x7f"] = actionDeleteWordBack

	// xterm-style CSI sequences for Ctrl+Backspace
	e.keySeqs["\x1b[3;5~"] = actionDeleteWordBack
	e.keySeqs["\x1b[3^"] = actionDeleteWordBack // rxvt
}

func historyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".lash_history")
}

func (e *LineEditor) loadHistory() {
	path := historyPath()
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	var entries []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			parts := strings.SplitN(line, " ", 2)
			if len(parts) == 2 {
				_, err := strconv.ParseInt(parts[0][1:], 10, 64)
				if err == nil {
					cmd := parts[1]
					if cmd != "" {
						entries = append(entries, cmd)
					}
					continue
				}
			}
			continue
		}
		if strings.TrimSpace(line) != "" {
			entries = append(entries, strings.TrimSpace(line))
		}
	}

	limit := e.config.HistorySize
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	e.history = entries
}

func (e *LineEditor) saveHistory(command string) {
	path := historyPath()
	if path == "" {
		return
	}

	dir := filepath.Dir(path)
	os.MkdirAll(dir, 0755)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	fmt.Fprintf(f, "#%d %s\n", time.Now().Unix(), command)
	f.Close()

	if len(e.history) > e.config.HistorySize {
		e.trimHistoryFile()
	}
}

func (e *LineEditor) trimHistoryFile() {
	path := historyPath()
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	f.Close()

	limit := e.config.HistorySize
	if len(lines) <= limit {
		return
	}
	lines = lines[len(lines)-limit:]

	f, err = os.Create(path)
	if err != nil {
		return
	}
	for _, l := range lines {
		fmt.Fprintln(f, l)
	}
	f.Close()
}

func isTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func (e *LineEditor) ReadLine(prompt string) (string, error) {
	if !isTerminal() {
		return e.readLineFallback(prompt)
	}
	return e.readLineRaw(prompt)
}

func (e *LineEditor) readLineFallback(prompt string) (string, error) {
	fmt.Print(prompt)
	if stdinReader != nil {
		line, err := stdinReader.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(line, "\n"), nil
	}
	if e.reader == nil {
		e.reader = bufio.NewReader(os.Stdin)
	}
	line, err := e.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\n"), nil
}

func getTermWidth() int {
	type winsize struct {
		ws_row, ws_col       uint16
		ws_xpixel, ws_ypixel uint16
	}
	ws := &winsize{}
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL,
		uintptr(os.Stdout.Fd()), syscall.TIOCGWINSZ,
		uintptr(unsafe.Pointer(ws)))
	if err != 0 {
		return 80
	}
	return int(ws.ws_col)
}

func visibleWidth(s string) int {
	w := 0
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			i += 2
			for i < len(runes) {
				if runes[i] >= 0x40 && runes[i] <= 0x7e {
					break
				}
				i++
			}
			continue
		}
		if runes[i] == '\n' || runes[i] == '\r' {
			if runes[i] == '\n' {
				w = 0
			}
		} else {
			w += runeWidth(runes[i])
		}
	}
	return w
}

func lastLineWidth(s string) int {
	lastNl := strings.LastIndex(s, "\n")
	if lastNl >= 0 {
		s = s[lastNl+1:]
	}
	return visibleWidth(s)
}

func firstLineWidth(s string) int {
	nlIdx := strings.Index(s, "\n")
	if nlIdx >= 0 {
		s = s[:nlIdx]
	}
	return visibleWidth(s)
}

func runeWidth(r rune) int {
	if r < 0x20 || r == 0x7f {
		return 0
	}
	if r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe10 && r <= 0xfe19) ||
		(r >= 0xfe30 && r <= 0xfe6f) ||
		(r >= 0xff01 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x20000 && r <= 0x2fffd) ||
		(r >= 0x30000 && r <= 0x3fffd)) {
		return 2
	}
	return 1
}

func bufWidth(buf []rune) int {
	w := 0
	for _, r := range buf {
		w += runeWidth(r)
	}
	return w
}

func (e *LineEditor) readLineRaw(prompt string) (string, error) {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	e.buf = nil
	e.cursor = 0
	e.screenRow = 0
	e.histIdx = len(e.history)
	prevW := 0

	os.Stdout.Write([]byte(prompt))

	for {
		var b [1]byte
		n, err := os.Stdin.Read(b[:])
		if err != nil {
			return "", err
		}
		if n == 0 {
			continue
		}

		if b[0] == '\x1b' {
			e.handleEscape(prompt, &prevW)
			continue
		}

		switch b[0] {
		case '\r', '\n':
			os.Stdout.Write([]byte("\r\n"))
			line := string(e.buf)
			if strings.TrimSpace(line) != "" {
				if len(e.history) == 0 || e.history[len(e.history)-1] != line {
					e.history = append(e.history, line)
					e.saveHistory(line)
				}
			}
			return line, nil

		case 127, 8:
			if e.cursor > 0 {
				e.buf = append(e.buf[:e.cursor-1], e.buf[e.cursor:]...)
				e.cursor--
				prevW = e.redraw(prompt, prevW)
			}

		case 3:
			os.Stdout.Write([]byte("^C\r\n"))
			e.buf = nil
			e.cursor = 0
			return "\x03", nil

		case 4:
			if len(e.buf) == 0 {
				return "", io.EOF
			}
			if e.cursor < len(e.buf) {
				e.buf = append(e.buf[:e.cursor], e.buf[e.cursor+1:]...)
				prevW = e.redraw(prompt, prevW)
			}

		case 23:
			e.deleteWordBack(prompt, &prevW)

		case 21:
			e.buf = e.buf[e.cursor:]
			e.cursor = 0
			prevW = e.redraw(prompt, prevW)

		case 11:
			e.buf = e.buf[:e.cursor]
			prevW = e.redraw(prompt, prevW)

		case 1:
			if e.cursor > 0 {
				e.cursor = 0
				prevW = e.redraw(prompt, prevW)
			}

		case 5:
			if e.cursor < len(e.buf) {
				e.cursor = len(e.buf)
				prevW = e.redraw(prompt, prevW)
			}

		case 12:
			os.Stdout.Write([]byte("\x1b[2J\x1b[H"))
			os.Stdout.Write([]byte(prompt))
			e.screenRow = 0
			prevW = e.redraw(prompt, 0)

		case 9:
			prevW = e.handleTabCompletion(prompt, prevW)

		case 18:
			prevW = e.handleReverseSearch(prompt, prevW)
			if e.accepted {
				e.accepted = false
				if e.buf == nil {
					return "\x03", nil
				}
				line := string(e.buf)
				return line, nil
			}

		default:
			if b[0] >= 32 {
				r, _ := e.readRune(b[0])
				e.buf = append(e.buf[:e.cursor], append([]rune{r}, e.buf[e.cursor:]...)...)
				e.cursor++
				prevW = e.redraw(prompt, prevW)
			}
		}
	}
}

func (e *LineEditor) readByte() byte {
	var b [1]byte
	n, _ := os.Stdin.Read(b[:])
	if n == 0 {
		return 0
	}
	return b[0]
}

func (e *LineEditor) readRune(first byte) (rune, bool) {
	var need int
	switch {
	case first < 0x80:
		return rune(first), false
	case first < 0xC0:
		return utf8.RuneError, true
	case first < 0xE0:
		need = 2
	case first < 0xF0:
		need = 3
	default:
		need = 4
	}
	buf := make([]byte, need)
	buf[0] = first
	for i := 1; i < need; i++ {
		b := e.readByte()
		if b == 0 {
			return utf8.RuneError, false
		}
		buf[i] = b
	}
	r, _ := utf8.DecodeRune(buf)
	return r, r == utf8.RuneError
}

func (e *LineEditor) handleEscape(prompt string, prevW *int) {
	b2 := e.readByte()
	if b2 == '[' {
		e.handleCSI(prompt, prevW)
	} else if b2 == 'O' {
		e.handleSS3(prompt, prevW)
	} else if b2 == '\x7f' || b2 == 8 {
		// ESC + DEL/BS = Ctrl+Backspace on many terminals
		e.deleteWhitespaceWordBack(prompt, prevW)
	}
}

// lookupKeyAction checks if a given byte sequence matches a known
// terminfo-based key binding and executes the corresponding action.
// Returns true if the sequence was handled.
func (e *LineEditor) lookupKeyAction(prompt string, prevW *int, seq string) bool {
	action, ok := e.keySeqs[seq]
	if !ok {
		return false
	}
	switch action {
	case actionDeleteWordBack:
		e.deleteWhitespaceWordBack(prompt, prevW)
	default:
		return false
	}
	return true
}

func (e *LineEditor) handleCSI(prompt string, prevW *int) {
	var params []int
	var current int
	var seqBuf strings.Builder
	seqBuf.WriteByte('[')
	for {
		b := e.readByte()
		seqBuf.WriteByte(b)
		if b >= '0' && b <= '9' {
			current = current*10 + int(b-'0')
		} else {
			params = append(params, current)
			current = 0
			switch b {
			case ';':
				continue
			case 'A':
				if len(e.history) > 0 && e.histIdx > 0 {
					e.histIdx--
					e.buf = []rune(e.history[e.histIdx])
					e.cursor = len(e.buf)
					*prevW = e.redraw(prompt, *prevW)
				}
			case 'B':
				if e.histIdx < len(e.history) {
					e.histIdx++
					if e.histIdx < len(e.history) {
						e.buf = []rune(e.history[e.histIdx])
					} else {
						e.buf = nil
					}
					e.cursor = len(e.buf)
					*prevW = e.redraw(prompt, *prevW)
				}
			case 'C':
				if len(params) >= 2 && params[1] == 5 {
					e.moveWordForward(prompt, prevW)
				} else {
					if e.cursor < len(e.buf) {
						e.cursor++
						*prevW = e.redraw(prompt, *prevW)
					}
				}
			case 'D':
				if len(params) >= 2 && params[1] == 5 {
					e.moveWordBack(prompt, prevW)
				} else {
					if e.cursor > 0 {
						e.cursor--
						*prevW = e.redraw(prompt, *prevW)
					}
				}
			case 'H':
				if len(params) >= 2 && params[1] == 5 {
					e.moveWordBack(prompt, prevW)
				} else {
					e.cursor = 0
					*prevW = e.redraw(prompt, *prevW)
				}
			case 'F':
				if len(params) >= 2 && params[1] == 5 {
					e.moveWordForward(prompt, prevW)
				} else {
					e.cursor = len(e.buf)
					*prevW = e.redraw(prompt, *prevW)
				}
			case '~':
				if len(params) > 0 {
					switch params[0] {
					case 1:
						e.cursor = 0
						*prevW = e.redraw(prompt, *prevW)
					case 3:
						if len(params) >= 2 && params[1] == 5 {
							e.deleteWhitespaceWordBack(prompt, prevW)
						} else if e.cursor < len(e.buf) {
							e.buf = append(e.buf[:e.cursor], e.buf[e.cursor+1:]...)
							*prevW = e.redraw(prompt, *prevW)
						}
					case 4:
						e.cursor = len(e.buf)
						*prevW = e.redraw(prompt, *prevW)
					}
				}
			default:
				// Check terminfo key sequences for unrecognized CSI keys
				if e.lookupKeyAction(prompt, prevW, "\x1b"+seqBuf.String()) {
					return
				}
			}
			return
		}
	}
}

func (e *LineEditor) handleSS3(prompt string, prevW *int) {
	b := e.readByte()
	switch b {
	case 'A':
		if len(e.history) > 0 && e.histIdx > 0 {
			e.histIdx--
			e.buf = []rune(e.history[e.histIdx])
			e.cursor = len(e.buf)
			*prevW = e.redraw(prompt, *prevW)
		}
	case 'B':
		if e.histIdx < len(e.history) {
			e.histIdx++
			if e.histIdx < len(e.history) {
				e.buf = []rune(e.history[e.histIdx])
			} else {
				e.buf = nil
			}
			e.cursor = len(e.buf)
			*prevW = e.redraw(prompt, *prevW)
		}
	case 'C':
		if e.cursor < len(e.buf) {
			e.cursor++
			*prevW = e.redraw(prompt, *prevW)
		}
	case 'D':
		if e.cursor > 0 {
			e.cursor--
			*prevW = e.redraw(prompt, *prevW)
		}
	case 'H':
		e.cursor = 0
		*prevW = e.redraw(prompt, *prevW)
	case 'F':
		e.cursor = len(e.buf)
		*prevW = e.redraw(prompt, *prevW)
	}
}

func (e *LineEditor) handleReverseSearch(prompt string, prevBufW int) int {
	savedBuf := make([]rune, len(e.buf))
	copy(savedBuf, e.buf)
	savedCursor := e.cursor

	var query []rune
	matchPos := -1

	prevBufW = e.redrawSearch(prompt, prevBufW, query, "")

	for {
		b := e.readByte()
		if b == 0 {
			e.buf = savedBuf
			e.cursor = savedCursor
			return e.redraw(prompt, prevBufW)
		}

		if b == '\x1b' {
			b2 := e.readByte()
			if b2 == '[' {
				final := e.readByte()
				for {
					if final < 'A' || final > 'Z' {
						if final != '~' {
							break
						}
						e.readByte()
					}
					break
				}
			}
			e.buf = savedBuf
			e.cursor = savedCursor
			return e.redraw(prompt, prevBufW)
		}

		switch b {
		case '\r', '\n':
			if matchPos >= 0 {
				e.buf = []rune(e.history[matchPos])
				e.cursor = len(e.buf)
			} else {
				e.buf = savedBuf
				e.cursor = savedCursor
			}
			os.Stdout.Write([]byte("\r\n"))
			line := string(e.buf)
			if strings.TrimSpace(line) != "" {
				if len(e.history) == 0 || e.history[len(e.history)-1] != line {
					e.history = append(e.history, line)
					e.saveHistory(line)
				}
			}
			e.accepted = true
			return bufWidth(e.buf)

		case 127, 8:
			if len(query) > 0 {
				query = query[:len(query)-1]
				matchPos = -1
				if len(query) > 0 {
					matchPos = e.findHistoryMatch(string(query), -1)
				}
				var matched string
				if matchPos >= 0 {
					matched = e.history[matchPos]
				}
				prevBufW = e.redrawSearch(prompt, prevBufW, query, matched)
			} else {
				e.buf = savedBuf
				e.cursor = savedCursor
				return e.redraw(prompt, prevBufW)
			}

		case 3:
			os.Stdout.Write([]byte("^C\r\n"))
			e.buf = nil
			e.cursor = 0
			e.accepted = true
			return 0

		case 7:
			e.buf = savedBuf
			e.cursor = savedCursor
			return e.redraw(prompt, prevBufW)

		case 18:
			if len(query) > 0 {
				newPos := e.findHistoryMatch(string(query), matchPos)
				if newPos >= 0 {
					matchPos = newPos
				}
				var matched string
				if matchPos >= 0 {
					matched = e.history[matchPos]
				}
				prevBufW = e.redrawSearch(prompt, prevBufW, query, matched)
			}

		default:
			if b >= 32 && b < 127 {
				query = append(query, rune(b))
				matchPos = e.findHistoryMatch(string(query), -1)
				var matched string
				if matchPos >= 0 {
					matched = e.history[matchPos]
				}
				prevBufW = e.redrawSearch(prompt, prevBufW, query, matched)
			} else if b >= 0x80 {
				r, _ := e.readRune(b)
				if r != utf8.RuneError {
					query = append(query, r)
					matchPos = e.findHistoryMatch(string(query), -1)
					var matched string
					if matchPos >= 0 {
						matched = e.history[matchPos]
					}
					prevBufW = e.redrawSearch(prompt, prevBufW, query, matched)
				}
			}
		}
	}
}

func (e *LineEditor) findHistoryMatch(query string, startPos int) int {
	lowerQ := strings.ToLower(query)
	start := len(e.history) - 1
	if startPos >= 0 {
		start = startPos - 1
	}
	for i := start; i >= 0; i-- {
		if strings.Contains(strings.ToLower(e.history[i]), lowerQ) {
			return i
		}
	}
	return -1
}

func (e *LineEditor) redrawSearch(prompt string, prevBufW int, query []rune, matched string) int {
	pvis := visibleWidth(prompt)
	termW := getTermWidth()
	if termW <= 0 {
		termW = 80
	}

	typingCol := pvis % termW

	prevRows := (typingCol + prevBufW + termW - 1) / termW
	if prevRows < 1 {
		prevRows = 1
	}

	var display string
	if matched != "" {
		display = fmt.Sprintf("bck-i-search: %s_%s", string(query), matched)
	} else {
		display = fmt.Sprintf("bck-i-search: %s_", string(query))
	}

	var buf strings.Builder

	if prevRows > 1 {
		buf.WriteString(fmt.Sprintf("\033[%dA", prevRows-1))
	}

	buf.WriteString("\r")
	if typingCol > 0 {
		buf.WriteString(fmt.Sprintf("\033[%dC", typingCol))
	}
	buf.WriteString("\033[K")

	for i := 1; i < prevRows; i++ {
		buf.WriteString("\033[B\r\033[K")
	}

	if prevRows > 1 {
		buf.WriteString(fmt.Sprintf("\033[%dA", prevRows-1))
	}

	buf.WriteString("\r")
	if typingCol > 0 {
		buf.WriteString(fmt.Sprintf("\033[%dC", typingCol))
	}
	buf.WriteString("\033[?25l")
	buf.WriteString(display)
	buf.WriteString("\033[?25h")
	os.Stdout.WriteString(buf.String())

	return visibleWidth(display)
}

func (e *LineEditor) moveWordBack(prompt string, prevW *int) {
	if e.cursor > 0 {
		pos := e.cursor
		if pos < len(e.buf) && e.buf[pos-1] != ' ' && (pos >= len(e.buf) || e.buf[pos] == ' ') {
			pos--
		}
		for pos > 0 && e.buf[pos-1] == ' ' {
			pos--
		}
		for pos > 0 && e.buf[pos-1] != ' ' {
			pos--
		}
		e.cursor = pos
		*prevW = e.redraw(prompt, *prevW)
	}
}

func (e *LineEditor) moveWordForward(prompt string, prevW *int) {
	if e.cursor < len(e.buf) {
		pos := e.cursor
		for pos < len(e.buf) && e.buf[pos] == ' ' {
			pos++
		}
		for pos < len(e.buf) && e.buf[pos] != ' ' {
			pos++
		}
		e.cursor = pos
		*prevW = e.redraw(prompt, *prevW)
	}
}

func (e *LineEditor) deleteWordBack(prompt string, prevW *int) {
	if e.cursor == 0 {
		return
	}
	pos := e.cursor
	for pos > 0 && e.buf[pos-1] == ' ' {
		pos--
	}
	for pos > 0 && e.buf[pos-1] != ' ' {
		pos--
	}
	e.buf = append(e.buf[:pos], e.buf[e.cursor:]...)
	e.cursor = pos
	*prevW = e.redraw(prompt, *prevW)
}

func (e *LineEditor) deleteWhitespaceWordBack(prompt string, prevW *int) {
	if e.cursor == 0 {
		return
	}
	pos := e.cursor
	for pos > 0 && (e.buf[pos-1] == ' ' || e.buf[pos-1] == '\t') {
		pos--
	}
	for pos > 0 && e.buf[pos-1] != ' ' && e.buf[pos-1] != '\t' {
		pos--
	}
	e.buf = append(e.buf[:pos], e.buf[e.cursor:]...)
	e.cursor = pos
	*prevW = e.redraw(prompt, *prevW)
}

func (e *LineEditor) redraw(prompt string, prevBufW int) int {
	pvis := visibleWidth(prompt)

	termW := getTermWidth()
	if termW <= 0 {
		termW = 80
	}

	typingCol := pvis % termW

	prevRows := (typingCol + prevBufW + termW - 1) / termW
	if prevRows < 1 {
		prevRows = 1
	}

	var buf strings.Builder

	if e.screenRow > 0 {
		buf.WriteString(fmt.Sprintf("\033[%dA", e.screenRow))
	}

	buf.WriteString("\r")
	if typingCol > 0 {
		buf.WriteString(fmt.Sprintf("\033[%dC", typingCol))
	}
	buf.WriteString("\033[K")

	for i := 1; i < prevRows; i++ {
		buf.WriteString("\033[B\r\033[K")
	}

	if prevRows > 1 {
		buf.WriteString(fmt.Sprintf("\033[%dA", prevRows-1))
	}

	buf.WriteString("\r")
	if typingCol > 0 {
		buf.WriteString(fmt.Sprintf("\033[%dC", typingCol))
	}

	var display string
	if e.config != nil && e.config.SyntaxColor && len(e.buf) > 0 {
		display = e.syntaxHighlight()
	} else {
		display = string(e.buf)
	}
	buf.WriteString("\033[?25l")
	buf.WriteString(display)

	newW := bufWidth(e.buf)
	cursorW := bufWidth(e.buf[:e.cursor])

	targetPos := typingCol + cursorW
	e.screenRow = targetPos / termW

	endPos := typingCol + newW
	var endRow int
	if endPos > 0 && endPos%termW == 0 {
		endRow = endPos/termW - 1
	} else {
		endRow = endPos / termW
	}

	rowsDiff := endRow - e.screenRow
	if rowsDiff > 0 {
		buf.WriteString(fmt.Sprintf("\033[%dA", rowsDiff))
	} else if rowsDiff < 0 {
		buf.WriteString(fmt.Sprintf("\033[%dB", -rowsDiff))
	}
	buf.WriteString("\r")
	targetCol := targetPos % termW
	if targetCol > 0 {
		buf.WriteString(fmt.Sprintf("\033[%dC", targetCol))
	}
	buf.WriteString("\033[?25h")
	os.Stdout.WriteString(buf.String())

	return newW
}

func (e *LineEditor) syntaxHighlight() string {
	text := string(e.buf)
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return text
	}

	inQuote := false
	quoteChar := rune(0)
	actualFirstEnd := 0
	for i, r := range e.buf {
		if !inQuote && (r == '\'' || r == '"') {
			inQuote = true
			quoteChar = r
		} else if inQuote && r == quoteChar {
			inQuote = false
		}
		actualFirstEnd = i + 1
		if !inQuote && (r == ' ' || r == '\t') {
			if i > 0 {
				actualFirstEnd = i
			}
			break
		}
	}

	cmdPart := string(e.buf[:actualFirstEnd])
	rest := string(e.buf[actualFirstEnd:])

	var result strings.Builder
	if isValidCommand(tokens[0]) {
		result.WriteString(colorGreen)
		result.WriteString(cmdPart)
		result.WriteString(colorReset)
	} else {
		result.WriteString(colorRed)
		result.WriteString(cmdPart)
		result.WriteString(colorReset)
	}

	if len(rest) > 0 {
		result.WriteString(highlightKeywords(rest))
	}

	return result.String()
}

func highlightKeywords(text string) string {
	runes := []rune(text)
	var buf strings.Builder
	i := 0

	for i < len(runes) {
		if runes[i] == ' ' || runes[i] == '\t' {
			buf.WriteRune(runes[i])
			i++
			continue
		}

		if runes[i] == '\'' {
			buf.WriteRune(runes[i])
			i++
			for i < len(runes) && runes[i] != '\'' {
				buf.WriteRune(runes[i])
				i++
			}
			if i < len(runes) {
				buf.WriteRune(runes[i])
				i++
			}
			continue
		}

		if runes[i] == '"' {
			buf.WriteRune(runes[i])
			i++
			for i < len(runes) && runes[i] != '"' {
				if runes[i] == '\\' && i+1 < len(runes) {
					buf.WriteRune(runes[i])
					buf.WriteRune(runes[i+1])
					i += 2
					continue
				}
				buf.WriteRune(runes[i])
				i++
			}
			if i < len(runes) {
				buf.WriteRune(runes[i])
				i++
			}
			continue
		}

		start := i
		for i < len(runes) && runes[i] != ' ' && runes[i] != '\t' {
			buf.WriteRune(runes[i])
			i++
		}
		token := string(runes[start:i])
		if isKeyword(token) {
			colored := colorYellow + token + colorReset
			bufStr := buf.String()
			buf.Reset()
			buf.WriteString(bufStr[:len(bufStr)-len(token)])
			buf.WriteString(colored)
		}
	}

	return buf.String()
}

func (e *LineEditor) handleTabCompletion(prompt string, prevBufW int) int {
	text := string(e.buf)
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return prevBufW
	}

	// Figure out which token the cursor is in and whether it's the first
	tokenIdx := -1
	inSpace := true
	for i, r := range e.buf {
		if i >= e.cursor {
			break
		}
		if r == ' ' || r == '\t' {
			inSpace = true
		} else if inSpace {
			inSpace = false
			tokenIdx++
		}
	}

	isFirstToken := tokenIdx == 0

	if inSpace && tokenIdx >= 0 {
		candidates := e.completePath("")
		if len(candidates) > 0 {
			os.Stdout.Write([]byte("\r\n"))
			for i, c := range candidates {
				if i > 0 && i%6 == 0 {
					os.Stdout.Write([]byte("\r\n"))
				} else if i > 0 {
					os.Stdout.Write([]byte("  "))
				}
				os.Stdout.Write([]byte(c))
			}
			os.Stdout.Write([]byte("\r\n"))
			os.Stdout.Write([]byte(prompt))
			e.screenRow = 0
			return e.redraw(prompt, 0)
		}
		return prevBufW
	}

	var partial string
	if isFirstToken {
		partial = tokens[0]
	} else {
		partial = ""
		if tokenIdx >= 0 && tokenIdx < len(tokens) {
			partial = tokens[tokenIdx]
		}
	}

	var candidates []string
	if isFirstToken {
		candidates = e.completeCommand(partial)
	} else {
		candidates = e.completePath(partial)
	}

	if len(candidates) == 0 {
		return prevBufW
	}

	// Find common prefix (case-insensitive)
	common := candidates[0]
	for _, c := range candidates[1:] {
		cRunes := []rune(c)
		commonRunes := []rune(common)
		for len(commonRunes) > 0 && !strings.EqualFold(string(cRunes[:min(len(commonRunes), len(cRunes))]), string(commonRunes[:min(len(commonRunes), len(cRunes))])) {
			commonRunes = commonRunes[:len(commonRunes)-1]
		}
		common = string(commonRunes)
	}

	if len(candidates) == 1 {
		completion := candidates[0]
		if !isFirstToken {
			info, err := os.Stat(e.resolvePartialPath(completion))
			if err == nil && info.IsDir() {
				if !strings.HasSuffix(completion, "/") {
					completion += "/"
				}
			} else {
				completion += " "
			}
		} else {
			completion += " "
		}
		partialRunes := utf8.RuneCountInString(partial)
		for i := 0; i < partialRunes && e.cursor > 0; i++ {
			e.buf = append(e.buf[:e.cursor-1], e.buf[e.cursor:]...)
			e.cursor--
		}
		for _, r := range completion {
			e.buf = append(e.buf[:e.cursor], append([]rune{r}, e.buf[e.cursor:]...)...)
			e.cursor++
		}
		return e.redraw(prompt, prevBufW)
	}

	// Multiple matches
	if common != partial {
		var commonActual string
		commonRunes := []rune(common)
		for _, c := range candidates {
			cRunes := []rune(c)
			if len(cRunes) >= len(commonRunes) && strings.EqualFold(string(cRunes[:len(commonRunes)]), common) {
				commonActual = string(cRunes[:len(commonRunes)])
				break
			}
		}
		if commonActual != "" && commonActual != partial {
			partialRunes := utf8.RuneCountInString(partial)
			for i := 0; i < partialRunes && e.cursor > 0; i++ {
				e.buf = append(e.buf[:e.cursor-1], e.buf[e.cursor:]...)
				e.cursor--
			}
			for _, r := range commonActual {
				e.buf = append(e.buf[:e.cursor], append([]rune{r}, e.buf[e.cursor:]...)...)
				e.cursor++
			}
			return e.redraw(prompt, prevBufW)
		}
	}

	// Show all candidates
	os.Stdout.Write([]byte("\r\n"))
	for i, c := range candidates {
		if i > 0 && i%6 == 0 {
			os.Stdout.Write([]byte("\r\n"))
		} else if i > 0 {
			os.Stdout.Write([]byte("  "))
		}
		os.Stdout.Write([]byte(c))
	}
	os.Stdout.Write([]byte("\r\n"))
	os.Stdout.Write([]byte(prompt))
	e.screenRow = 0
	return e.redraw(prompt, 0)
}

func (e *LineEditor) completeCommand(partial string) []string {
	var matches []string

	for _, cmd := range allBuiltins {
		if strings.HasPrefix(cmd, partial) {
			matches = append(matches, cmd)
		}
	}

	for _, kw := range allKeywords {
		if strings.HasPrefix(kw, partial) {
			matches = append(matches, kw)
		}
	}

	for _, name := range allAliasNames() {
		if strings.HasPrefix(name, partial) {
			matches = append(matches, name)
		}
	}

	path := os.Getenv("PATH")
	if path != "" {
		seen := make(map[string]bool)
		for _, dir := range filepath.SplitList(path) {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				name := entry.Name()
				if !strings.HasPrefix(name, partial) {
					continue
				}
				if seen[name] {
					continue
				}
				seen[name] = true
				fullPath := filepath.Join(dir, name)
				info, err := os.Stat(fullPath)
				if err != nil {
					continue
				}
				if info.Mode()&0111 != 0 && !info.IsDir() {
					matches = append(matches, name)
				}
			}
		}
	}

	sort.Strings(matches)
	return matches
}

func (e *LineEditor) completePath(partial string) []string {
	dir := "."
	prefix := partial

	// Handle tilde
	if strings.HasPrefix(partial, "~/") {
		home := os.Getenv("HOME")
		if home != "" {
			dir = home
			prefix = partial[2:]
		}
	} else if partial == "~" {
		home := os.Getenv("HOME")
		if home != "" {
			return []string{"~/"}
		}
		return nil
	} else if strings.Contains(partial, "/") {
		idx := strings.LastIndex(partial, "/")
		dir = partial[:idx]
		if dir == "" {
			dir = "/"
		}
		prefix = partial[idx+1:]
	}

	var matches []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.EqualFold(name[:min(len(prefix), len(name))], prefix) || len(name) < len(prefix) {
			continue
		}
		if len(prefix) == 0 && (name[0] == '.' || name[0] == '#') {
			continue
		}
		if len(prefix) > 0 && prefix[0] != '.' && name[0] == '.' {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.IsDir() {
			name += "/"
		}

		// Reconstruct full match relative to what user typed
		if strings.Contains(partial, "/") {
			idx := strings.LastIndex(partial, "/")
			name = partial[:idx+1] + name
		} else if strings.HasPrefix(partial, "~/") {
			name = "~/" + name
		} else if partial == "~" {
			name = "~/" + name
		}

		matches = append(matches, name)
	}

	sort.Strings(matches)
	return matches
}

func (e *LineEditor) resolvePartialPath(s string) string {
	if strings.HasPrefix(s, "~/") {
		home := os.Getenv("HOME")
		if home != "" {
			return filepath.Join(home, s[2:])
		}
	}
	return s
}
