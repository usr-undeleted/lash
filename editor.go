package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/term"
)

type completionEntry struct {
	name string
	desc string
}

type LineEditor struct {
	buf            []rune
	cursor         int
	history        []string
	histIdx        int
	config         *Config
	reader         *bufio.Reader
	accepted       bool
	screenRow      int
	eofCount       int
	dispatchMap    map[string]string
	termKeySeqs    map[string]string
	savedTermState *term.State
	continuation   bool
	suggestion    string
	cycleCandidates []string
	cycleIndex      int
	cycleLen        int
	cycleLastBuf    string
	menuActive    bool
	menuCandidates []completionEntry
	menuSelected  int
	menuRows      int
	lastDisplayRows int
}

func NewLineEditor(cfg *Config) *LineEditor {
	e := &LineEditor{config: cfg}
	e.initKeySequences()
	e.initDispatch()
	e.loadHistory()
	return e
}

// initDispatch builds the dispatch map from defaults, then merges
// terminfo sequences as lower-priority fallbacks.
func (e *LineEditor) initDispatch() {
	e.dispatchMap = make(map[string]string)
	for key, action := range defaultKeybinds {
		seq := keyNameToSequence(key)
		if seq != "" {
			e.dispatchMap[seq] = action
		}
	}
	for seq, action := range e.termKeySeqs {
		if _, exists := e.dispatchMap[seq]; !exists {
			e.dispatchMap[seq] = action
		}
	}
	if e.config != nil {
		for key, action := range e.config.Keybinds {
			seq := keyNameToSequence(key)
			if seq != "" && isValidAction(action) {
				e.dispatchMap[seq] = action
			}
		}
	}
}

// executeAction runs a named action on the editor.
// Returns (line, err, true) if the action terminates input (accept, eof, interrupt).
func (e *LineEditor) executeAction(action string, prompt string, prevW *int) (string, error, bool) {
	if action != actComplete {
		e.clearCycle()
	}
	switch action {
	case actBeginningOfLine:
		if e.cursor > 0 {
			e.cursor = 0
			*prevW = e.redraw(prompt, *prevW)
		}
	case actEndOfLine:
		if e.cursor < len(e.buf) {
			e.cursor = len(e.buf)
			e.clearSuggestion()
			*prevW = e.redraw(prompt, *prevW)
		} else if e.suggestion != "" {
			e.buf = append(e.buf, []rune(e.suggestion)...)
			e.cursor = len(e.buf)
			e.clearSuggestion()
			*prevW = e.redraw(prompt, *prevW)
		}
	case actKillLineStart:
		e.buf = e.buf[e.cursor:]
		e.cursor = 0
		e.updateSuggestion()
		*prevW = e.redraw(prompt, *prevW)
	case actKillLineEnd:
		e.buf = e.buf[:e.cursor]
		e.updateSuggestion()
		*prevW = e.redraw(prompt, *prevW)
	case actDeleteWordBack:
		e.deleteWordBack(prompt, prevW)
		e.updateSuggestion()
	case actDeleteWordWSBack:
		e.deleteWhitespaceWordBack(prompt, prevW)
		e.updateSuggestion()
	case actDeleteChar:
		if e.cursor < len(e.buf) {
			e.buf = append(e.buf[:e.cursor], e.buf[e.cursor+1:]...)
			e.updateSuggestion()
			*prevW = e.redraw(prompt, *prevW)
		}
	case actBackspace:
		if e.cursor > 0 {
			e.buf = append(e.buf[:e.cursor-1], e.buf[e.cursor:]...)
			e.cursor--
			e.updateSuggestion()
			*prevW = e.redraw(prompt, *prevW)
		}
	case actClearScreen:
		e.clearSuggestion()
		os.Stdout.Write([]byte("\x1b[2J\x1b[H"))
		os.Stdout.Write([]byte(prompt))
		e.screenRow = 0
		*prevW = e.redraw(prompt, 0)
	case actReverseSearch:
		e.clearSuggestion()
		*prevW = e.handleReverseSearch(prompt, *prevW)
		if e.accepted {
			e.accepted = false
			if e.buf == nil {
				return "\x03", nil, true
			}
			return string(e.buf), nil, true
		}
	case actAcceptLine:
		e.clearSuggestion()
		os.Stdout.Write([]byte("\r\n"))
		line := string(e.buf)
		if strings.TrimSpace(line) != "" {
			if setHistIgnoreSpace && len(line) > 0 && line[0] == ' ' {
				return line, nil, true
			}
			if setHistIgnoreDups {
				e.removeHistoryDup(line)
				e.history = append(e.history, line)
				e.saveHistory(line)
			} else {
				if len(e.history) == 0 || e.history[len(e.history)-1] != line {
					e.history = append(e.history, line)
					e.saveHistory(line)
				}
			}
		}
		return line, nil, true
	case actEOF:
		if len(e.buf) == 0 {
			if setIgnoreEOF {
				e.eofCount++
				if e.eofCount < 10 {
					os.Stdout.Write([]byte("\r\n"))
					fmt.Fprintf(os.Stdout, "Use \"exit\" to leave the shell.\r\n")
					os.Stdout.Write([]byte(prompt))
					e.screenRow = 0
					return "", nil, false
				}
			}
			return "", io.EOF, true
		}
		e.eofCount = 0
		if e.cursor < len(e.buf) {
			e.buf = append(e.buf[:e.cursor], e.buf[e.cursor+1:]...)
			e.updateSuggestion()
			*prevW = e.redraw(prompt, *prevW)
		}
	case actInterrupt:
		e.clearSuggestion()
		os.Stdout.Write([]byte("^C\r\n"))
		e.buf = nil
		e.cursor = 0
		return "\x03", nil, true
	case actSuspend:
		e.clearSuggestion()
		fd := int(os.Stdin.Fd())
		if e.savedTermState != nil {
			term.Restore(fd, e.savedTermState)
		}
		syscall.Kill(syscall.Getpid(), syscall.SIGTSTP)
		state, err := term.MakeRaw(fd)
		if err == nil {
			e.savedTermState = state
		}
		os.Stdout.Write([]byte(prompt))
		e.screenRow = 0
		*prevW = e.redraw(prompt, 0)
	case actWordBack:
		e.moveWordBack(prompt, prevW)
	case actWordForward:
		e.moveWordForward(prompt, prevW)
	case actHistoryBack:
		if len(e.history) > 0 && e.histIdx > 0 {
			e.histIdx--
			e.buf = []rune(e.history[e.histIdx])
			e.cursor = len(e.buf)
			e.clearSuggestion()
			*prevW = e.redraw(prompt, *prevW)
		}
	case actHistoryForward:
		if e.histIdx < len(e.history) {
			e.histIdx++
			if e.histIdx < len(e.history) {
				e.buf = []rune(e.history[e.histIdx])
			} else {
				e.buf = nil
			}
			e.cursor = len(e.buf)
			e.clearSuggestion()
			*prevW = e.redraw(prompt, *prevW)
		}
	case actComplete:
		e.clearSuggestion()
		*prevW = e.handleTabCompletion(prompt, *prevW)
	case actCursorLeft:
		if e.cursor > 0 {
			e.cursor--
			*prevW = e.redraw(prompt, *prevW)
		}
	case actCursorRight:
		if e.cursor < len(e.buf) {
			e.cursor++
			e.clearSuggestion()
			*prevW = e.redraw(prompt, *prevW)
		} else if e.suggestion != "" {
			runes := []rune(e.suggestion)
			e.buf = append(e.buf, runes[0])
			e.cursor++
			e.suggestion = string(runes[1:])
			e.updateSuggestion()
			*prevW = e.redraw(prompt, *prevW)
		}
	case actNop:
	}
	return "", nil, false
}

// initKeySequences registers terminal-specific key sequences that vary
// by terminal emulator. These are merged into the dispatch map as fallbacks.
func (e *LineEditor) initKeySequences() {
	e.termKeySeqs = make(map[string]string)
	e.termKeySeqs["\x1b\x7f"] = actDeleteWordWSBack
	e.termKeySeqs["\x1b[3;5~"] = actDeleteWordWSBack
	e.termKeySeqs["\x1b[3^"] = actDeleteWordWSBack
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

func (e *LineEditor) removeHistoryDup(line string) {
	for i, h := range e.history {
		if h == line {
			e.history = append(e.history[:i], e.history[i+1:]...)
			e.rewriteHistoryFile()
			break
		}
	}
}

func (e *LineEditor) rewriteHistoryFile() {
	path := historyPath()
	if path == "" {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		return
	}
	for _, h := range e.history {
		fmt.Fprintf(f, "#%d %s\n", time.Now().Unix(), h)
	}
	f.Close()
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

// check if the last command output ended with a newline
// sends DSR (Device Status Report) and reads cursor position response from stdin
func checkTrailingNewline() bool {
	outFd := int(os.Stdout.Fd())

	os.Stdout.WriteString("\033[6n")
	syscall.Syscall(syscall.SYS_IOCTL, uintptr(outFd), 0x5402, uintptr(0x1))

	os.Stdin.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	defer os.Stdin.SetReadDeadline(time.Time{})

	buf := make([]byte, 0, 32)
	tmp := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[0])
			if tmp[0] == 'R' {
				break
			}
		}
		if err != nil {
			break
		}
	}

	resp := string(buf)
	if !strings.HasPrefix(resp, "\x1b[") {
		return false
	}
	resp = resp[2:]
	if len(resp) == 0 || resp[len(resp)-1] != 'R' {
		return false
	}
	resp = resp[:len(resp)-1]
	parts := strings.SplitN(resp, ";", 2)
	if len(parts) < 2 {
		return false
	}
	col, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return col > 1
}

const promptCRIndicator = "\x1b[7m%\x1b[0m"

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
	e.savedTermState = oldState
	e.clearSuggestion()

	e.buf = nil
	e.cursor = 0
	e.screenRow = 0
	e.lastDisplayRows = 0
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

		if e.menuActive {
			if b[0] == '\x1b' {
				b2 := e.readByte()
				if b2 == '[' {
					b3 := e.readByte()
					if b3 == 'A' {
						if e.menuSelected > 0 {
							e.menuSelected--
						} else {
							e.menuSelected = len(e.menuCandidates) - 1
						}
						prevW = e.renderMenu(prompt, prevW)
						continue
					}
					if b3 == 'B' {
						if e.menuSelected < len(e.menuCandidates)-1 {
							e.menuSelected++
						} else {
							e.menuSelected = 0
						}
						prevW = e.renderMenu(prompt, prevW)
						continue
					}
					if b3 == 'D' {
						menuCols := 1
						menuRows := 1
						if e.menuCandidates != nil && len(e.menuCandidates) > 0 {
							menuCols = len(e.menuCandidates)
							menuRows = 1
							termW2 := getTermWidth()
							if termW2 <= 0 {
								termW2 = 80
							}
							mnl := 0
							for _, c := range e.menuCandidates {
								w := visibleWidth(c.name)
								if w > mnl {
									mnl = w
								}
							}
							cw := mnl + 2
							if cw <= termW2 {
								menuCols = termW2 / cw
							}
							if menuCols < 1 {
								menuCols = 1
							}
							menuRows = (len(e.menuCandidates) + menuCols - 1) / menuCols
							if menuRows > 10 {
								menuCols = (len(e.menuCandidates) + 9) / 10
								menuRows = (len(e.menuCandidates) + menuCols - 1) / menuCols
							}
						}
						newSel := e.menuSelected - menuRows
						if newSel < 0 {
							col := e.menuSelected
							itemsInPrevCol := col + menuRows
							for itemsInPrevCol > len(e.menuCandidates) {
								itemsInPrevCol -= menuRows
							}
							newSel = itemsInPrevCol - menuRows
							if newSel < 0 {
								newSel = len(e.menuCandidates) - 1
							}
						}
						if newSel >= 0 && newSel < len(e.menuCandidates) {
							e.menuSelected = newSel
						}
						prevW = e.renderMenu(prompt, prevW)
						continue
					}
					if b3 == 'C' {
						menuCols := 1
						menuRows := 1
						if e.menuCandidates != nil && len(e.menuCandidates) > 0 {
							menuCols = len(e.menuCandidates)
							menuRows = 1
							termW2 := getTermWidth()
							if termW2 <= 0 {
								termW2 = 80
							}
							mnl := 0
							for _, c := range e.menuCandidates {
								w := visibleWidth(c.name)
								if w > mnl {
									mnl = w
								}
							}
							cw := mnl + 2
							if cw <= termW2 {
								menuCols = termW2 / cw
							}
							if menuCols < 1 {
								menuCols = 1
							}
							menuRows = (len(e.menuCandidates) + menuCols - 1) / menuCols
							if menuRows > 10 {
								menuCols = (len(e.menuCandidates) + 9) / 10
								menuRows = (len(e.menuCandidates) + menuCols - 1) / menuCols
							}
						}
						newSel := e.menuSelected + menuRows
						if newSel >= len(e.menuCandidates) {
							col := e.menuSelected / menuRows
							newSel = col * menuRows
							if newSel >= len(e.menuCandidates) {
								newSel = 0
							}
						}
						if newSel >= 0 && newSel < len(e.menuCandidates) {
							e.menuSelected = newSel
						}
						prevW = e.renderMenu(prompt, prevW)
						continue
					}
				}
				prevW = e.cancelMenu(prompt, prevW)
				continue
			}
			if b[0] == '\r' || b[0] == '\n' {
				prevW = e.acceptMenuSelection(prompt, prevW)
				e.clearSuggestion()
				os.Stdout.Write([]byte("\r\n"))
				line := string(e.buf)
				if strings.TrimSpace(line) != "" {
					if setHistIgnoreSpace && len(line) > 0 && line[0] == ' ' {
						return line, nil
					}
					if setHistIgnoreDups {
						e.removeHistoryDup(line)
						e.history = append(e.history, line)
						e.saveHistory(line)
					} else {
						if len(e.history) == 0 || e.history[len(e.history)-1] != line {
							e.history = append(e.history, line)
							e.saveHistory(line)
						}
					}
				}
				return line, nil
			}
			if b[0] == 3 {
				e.cancelMenu(prompt, prevW)
				os.Stdout.Write([]byte("^C\r\n"))
				e.buf = nil
				e.cursor = 0
				return "\x03", nil
			}
			if b[0] == 4 {
				e.cancelMenu(prompt, prevW)
				if len(e.buf) == 0 {
					if setIgnoreEOF {
						e.eofCount++
						if e.eofCount < 10 {
							os.Stdout.Write([]byte("\r\n"))
							fmt.Fprintf(os.Stdout, "Use \"exit\" to leave the shell.\r\n")
							os.Stdout.Write([]byte(prompt))
							e.screenRow = 0
							continue
						}
					}
					return "", io.EOF
				}
				e.eofCount = 0
				if e.cursor < len(e.buf) {
					e.buf = append(e.buf[:e.cursor], e.buf[e.cursor+1:]...)
					e.updateSuggestion()
					prevW = e.redraw(prompt, prevW)
				}
				continue
			}
			if b[0] == 127 || b[0] == 8 {
				e.cancelMenu(prompt, prevW)
				if e.cursor > 0 {
					e.buf = append(e.buf[:e.cursor-1], e.buf[e.cursor:]...)
					e.cursor--
					e.updateSuggestion()
					prevW = e.redraw(prompt, prevW)
				}
				continue
			}
			if b[0] >= 32 {
				e.cancelMenu(prompt, prevW)
				r, _ := e.readRune(b[0])
				e.buf = append(e.buf[:e.cursor], append([]rune{r}, e.buf[e.cursor:]...)...)
				e.cursor++
				e.updateSuggestion()
				prevW = e.redraw(prompt, prevW)
				continue
			}
			seq := string([]byte{b[0]})
			if action, ok := e.dispatchMap[seq]; ok {
				e.cancelMenu(prompt, prevW)
				if result, err, done := e.executeAction(action, prompt, &prevW); done {
					return result, err
				}
				continue
			}
			e.cancelMenu(prompt, prevW)
			continue
		}

		if b[0] == '\x1b' {
			if result, err, handled := e.handleEscape(prompt, &prevW); handled {
				return result, err
			}
			continue
		}

		if b[0] != 4 {
			e.eofCount = 0
		}

		seq := string([]byte{b[0]})
		if action, ok := e.dispatchMap[seq]; ok {
			if result, err, done := e.executeAction(action, prompt, &prevW); done {
				return result, err
			}
			continue
		}

		if b[0] >= 32 {
			r, _ := e.readRune(b[0])
			e.buf = append(e.buf[:e.cursor], append([]rune{r}, e.buf[e.cursor:]...)...)
			e.cursor++
			e.updateSuggestion()
			prevW = e.redraw(prompt, prevW)
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

func (e *LineEditor) handleEscape(prompt string, prevW *int) (string, error, bool) {
	b2 := e.readByte()
	if b2 == '[' {
		return e.handleCSI(prompt, prevW)
	} else if b2 == 'O' {
		return e.handleSS3(prompt, prevW)
	} else if b2 == '\x7f' || b2 == 8 {
		e.deleteWhitespaceWordBack(prompt, prevW)
		return "", nil, false
	}
	fullSeq := "\x1b" + string([]byte{b2})
	if action, ok := e.dispatchMap[fullSeq]; ok {
		return e.executeAction(action, prompt, prevW)
	}
	return "", nil, false
}

func (e *LineEditor) handleCSI(prompt string, prevW *int) (string, error, bool) {
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
			if b == ';' {
				continue
			}
			fullSeq := "\x1b" + seqBuf.String()
			if action, ok := e.dispatchMap[fullSeq]; ok {
				if result, err, done := e.executeAction(action, prompt, prevW); done {
					return result, err, true
				}
				return "", nil, false
			}
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
				if len(params) >= 2 && params[1] == 5 {
					e.moveWordForward(prompt, prevW)
				} else {
					if e.cursor < len(e.buf) {
						e.cursor++
						e.clearSuggestion()
						*prevW = e.redraw(prompt, *prevW)
					} else if e.suggestion != "" {
						runes := []rune(e.suggestion)
						e.buf = append(e.buf, runes...)
						e.cursor += len(runes)
						e.suggestion = ""
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
					if e.cursor < len(e.buf) {
						e.cursor = len(e.buf)
						e.clearSuggestion()
						*prevW = e.redraw(prompt, *prevW)
					} else if e.suggestion != "" {
						e.buf = append(e.buf, []rune(e.suggestion)...)
						e.cursor = len(e.buf)
						e.clearSuggestion()
						*prevW = e.redraw(prompt, *prevW)
					}
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
						if e.cursor < len(e.buf) {
							e.cursor = len(e.buf)
							e.clearSuggestion()
							*prevW = e.redraw(prompt, *prevW)
						} else if e.suggestion != "" {
							e.buf = append(e.buf, []rune(e.suggestion)...)
							e.cursor = len(e.buf)
							e.clearSuggestion()
							*prevW = e.redraw(prompt, *prevW)
						}
					}
				}
			}
			return "", nil, false
		}
	}
}

func (e *LineEditor) handleSS3(prompt string, prevW *int) (string, error, bool) {
	b := e.readByte()
	fullSeq := "\x1bO" + string([]byte{b})
	if action, ok := e.dispatchMap[fullSeq]; ok {
		if result, err, done := e.executeAction(action, prompt, prevW); done {
			return result, err, true
		}
		return "", nil, false
	}
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
	return "", nil, false
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
				if setHistIgnoreSpace && len(line) > 0 && line[0] == ' ' {
					e.accepted = true
					return bufWidth(e.buf)
				}
				if setHistIgnoreDups {
					e.removeHistoryDup(line)
					e.history = append(e.history, line)
					e.saveHistory(line)
				} else {
					if len(e.history) == 0 || e.history[len(e.history)-1] != line {
						e.history = append(e.history, line)
						e.saveHistory(line)
					}
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

	prevRows := e.lastDisplayRows
	if prevRows < 1 {
		prevRows = 1
	}

	newW := bufWidth(e.buf)
	inputRows := (typingCol + newW + termW - 1) / termW
	if inputRows < 1 {
		inputRows = 1
	}

	sugW := 0
	if e.suggestion != "" {
		sugW = bufWidth([]rune(e.suggestion))
	}
	totalW := newW + sugW
	totalRows := (typingCol + totalW + termW - 1) / termW
	if totalRows < 1 {
		totalRows = 1
	}

	rowsToClear := prevRows
	if inputRows > rowsToClear {
		rowsToClear = inputRows
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

	for i := 1; i < rowsToClear; i++ {
		buf.WriteString("\033[B\r\033[K")
	}

	if rowsToClear > 1 {
		buf.WriteString(fmt.Sprintf("\033[%dA", rowsToClear-1))
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
	if e.suggestion != "" {
		buf.WriteString(colorDarkGrey)
		buf.WriteString(e.suggestion)
		buf.WriteString(colorReset)
		buf.WriteString("\033[K")
	}

	cursorW := bufWidth(e.buf[:e.cursor])

	targetPos := typingCol + cursorW
	e.screenRow = targetPos / termW

	var physRow int
	if e.suggestion != "" {
		sugEndPos := typingCol + totalW
		if sugEndPos > 0 && sugEndPos%termW == 0 {
			physRow = sugEndPos/termW - 1
		} else {
			physRow = sugEndPos / termW
		}
	} else {
		endPos := typingCol + newW
		if endPos > 0 && endPos%termW == 0 {
			physRow = endPos/termW - 1
		} else {
			physRow = endPos / termW
		}
	}

	rowsDiff := physRow - e.screenRow
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

	e.lastDisplayRows = totalRows
	return newW
}

func (e *LineEditor) clearSuggestion() {
	e.suggestion = ""
}

func (e *LineEditor) clearCycle() {
	e.cycleCandidates = nil
}

func (e *LineEditor) updateSuggestion() {
	if e.config == nil || !e.config.AutoSuggest {
		e.suggestion = ""
		return
	}
	if len(e.buf) == 0 || e.cursor == 0 {
		e.suggestion = ""
		return
	}

	prefix := string(e.buf[:e.cursor])

	for i := len(e.history) - 1; i >= 0; i-- {
		entry := e.history[i]
		if strings.HasPrefix(entry, prefix) && len(entry) > len(prefix) {
			e.suggestion = entry[len(prefix):]
			return
		}
	}

	tokens := tokenize(prefix)
	if len(tokens) == 0 {
		e.suggestion = ""
		return
	}

	tokenIdx := -1
	inSpace := true
	for _, r := range prefix {
		if r == ' ' || r == '\t' {
			inSpace = true
		} else if inSpace {
			inSpace = false
			tokenIdx++
		}
	}

	if inSpace || tokenIdx < 0 || tokenIdx >= len(tokens) {
		e.suggestion = ""
		return
	}

	partial := tokens[tokenIdx]
	if partial == "" {
		e.suggestion = ""
		return
	}

	var candidates []completionEntry
	if tokenIdx == 0 {
		candidates = e.completeCommand(partial)
	} else {
		candidates = e.completePath(partial, len(tokens) > 0 && tokens[0] == "cd")
	}

	if len(candidates) == 1 && strings.HasPrefix(candidates[0].name, partial) && len(candidates[0].name) > len(partial) {
		e.suggestion = candidates[0].name[len(partial):]
	} else {
		e.suggestion = ""
	}
}

func isChainOp(t string) bool {
	switch t {
	case "|", "&&", "||":
		return true
	}
	return false
}

func isSeparatorOp(t string) bool {
	switch t {
	case ";", "&", ";;":
		return true
	}
	return false
}

func isRedirectionOp(t string) bool {
	switch t {
	case ">>", ">", "<", "<<", "<<-", "<<<", ">|":
		return true
	}
	return false
}

func writeColored(b *strings.Builder, text, color string) {
	if color != "" {
		b.WriteString(color)
	}
	b.WriteString(text)
	if color != "" {
		b.WriteString(colorReset)
	}
}

func isVarStartChar(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_'
}

// colorArgParts colors sub-tokens inside an argument (variables, escapes, tilde, globs)
func hasUnquotedGlob(token string) bool {
	inSingle := false
	inDouble := false
	for _, r := range token {
		if r == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if r == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if inSingle || inDouble {
			continue
		}
		if r == '*' || r == '?' || r == '[' {
			return true
		}
	}
	return false
}

func colorArgParts(b *strings.Builder, token string) {
	if hasUnquotedGlob(token) {
		writeColored(b, token, colorMagenta)
		return
	}
	inSingle := false
	inDouble := false
	i := 0
	for i < len(token) {
		r := rune(token[i])
		if r == '\'' && !inDouble {
			writeColored(b, "'", colorMagenta)
			inSingle = !inSingle
			i++
			continue
		}
		if r == '"' && !inSingle {
			writeColored(b, `"`, colorMagenta)
			inDouble = !inDouble
			i++
			continue
		}
		if inSingle {
			b.WriteByte(token[i])
			i++
			continue
		}
		if r == '\\' && inDouble && i+1 < len(token) {
			writeColored(b, string(token[i:i+2]), colorCyan)
			i += 2
			continue
		}
		if r == '\\' && !inDouble {
			writeColored(b, string(token[i:i+2]), colorCyan)
			i += 2
			continue
		}
		if r == '$' && i+1 < len(token) {
			next := rune(token[i+1])
			if next == '{' || next == '(' || isVarStartChar(next) || next == '?' || next == '$' || next == '!' || next == '#' {
				end := i + 1
				if next == '{' {
					end++
					for end < len(token) && token[end] != '}' {
						end++
					}
					if end < len(token) {
						end++
					}
				} else if next == '(' {
					depth := 1
					end++
					for end < len(token) && depth > 0 {
						if token[end] == '(' {
							depth++
						} else if token[end] == ')' {
							depth--
						}
						end++
					}
				} else {
					end++
					for end < len(token) && (isVarStartChar(rune(token[end])) || (token[end] >= '0' && token[end] <= '9')) {
						end++
					}
				}
				writeColored(b, token[i:end], colorCyan)
				i = end
				continue
			}
		}
		if (r == '*' || r == '?' || r == '[') && !inDouble {
			writeColored(b, string(r), colorMagenta)
			i++
			continue
		}
		b.WriteByte(token[i])
		i++
	}
}

func looksLikePath(token string) bool {
	return strings.HasPrefix(token, "/") || strings.HasPrefix(token, "./") ||
		strings.HasPrefix(token, "../") || strings.HasPrefix(token, "~") ||
		strings.Contains(token, "/")
}

func isSimpleToken(token string) bool {
	for _, r := range token {
		switch r {
		case '$', '\\', '\'', '"', '*', '?', '[':
			return false
		}
	}
	return true
}

func colorPathOrArg(b *strings.Builder, token string) {
	if !isSimpleToken(token) {
		colorArgParts(b, token)
		return
	}
	resolved := resolveTildeForCheck(token)
	info, err := os.Stat(resolved)
	if err != nil {
		if looksLikePath(token) {
			writeColored(b, token, colorRed)
		} else {
			b.WriteString(token)
		}
		return
	}
	if info.IsDir() {
		writeColored(b, token, colorBold+colorBlue)
	} else {
		writeColored(b, token, colorCyan)
	}
}

func resolveTildeForCheck(name string) string {
	if strings.HasPrefix(name, "~/") {
		home := os.Getenv("HOME")
		if home != "" {
			return home + name[1:]
		}
	}
	return name
}

func (e *LineEditor) syntaxHighlight() string {
	runes := e.buf
	if len(runes) == 0 || e.continuation {
		return string(runes)
	}

	type span struct {
		start, end int
	}
	var spans []span
	i := 0
	for i < len(runes) {
		for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
			i++
		}
		if i >= len(runes) {
			break
		}
		start := i
		if runes[i] == '\'' {
			i++
			for i < len(runes) && runes[i] != '\'' {
				i++
			}
			if i < len(runes) {
				i++
			}
		} else if runes[i] == '"' {
			i++
			for i < len(runes) && runes[i] != '"' {
				if runes[i] == '\\' && i+1 < len(runes) {
					i += 2
				} else {
					i++
				}
			}
			if i < len(runes) {
				i++
			}
		} else if runes[i] == '#' {
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
		} else {
			for i < len(runes) && runes[i] != ' ' && runes[i] != '\t' {
				i++
			}
		}
		spans = append(spans, span{start, i})
	}
	if len(spans) == 0 {
		return string(runes)
	}

	cmdPos := true
	optionMode := false
	redirNext := false
	var result strings.Builder

	for idx, sp := range spans {
		token := string(runes[sp.start:sp.end])

		if token == "#" {
			writeColored(&result, token+string(runes[sp.end:]), colorDarkGrey)
			break
		}
		if strings.HasPrefix(token, "#") {
			writeColored(&result, token, colorDarkGrey)
			if idx < len(spans)-1 {
				result.WriteString(string(runes[sp.end:spans[idx+1].start]))
			}
			continue
		}

		if isChainOp(token) {
			writeColored(&result, token, colorBold)
			cmdPos = true
			optionMode = false
			redirNext = false
		} else if isSeparatorOp(token) {
			writeColored(&result, token, colorDarkGrey)
			cmdPos = true
			optionMode = false
			redirNext = false
		} else if isRedirectionOp(token) {
			writeColored(&result, token, colorCyan)
			redirNext = true
			optionMode = false
		} else if cmdPos {
			if isKeyword(token) {
				writeColored(&result, token, colorYellow)
				if token == "then" || token == "do" || token == "else" || token == "elif" || token == "!" {
					cmdPos = true
				} else {
					cmdPos = false
				}
			} else {
				resolved := resolveTildeForCheck(token)
				valid := isValidCommand(resolved)
				if !valid && e.config != nil && e.config.AutoCd {
					if info, err := os.Stat(resolved); err == nil && info.IsDir() {
						valid = true
					}
				}
				if valid {
					writeColored(&result, token, colorGreen)
				} else {
					writeColored(&result, token, colorRed)
				}
				cmdPos = false
			}
			optionMode = false
			redirNext = false
		} else if redirNext {
			colorPathOrArg(&result, token)
			redirNext = false
		} else if optionMode && strings.HasPrefix(token, "-") {
			writeColored(&result, token, colorCyan)
		} else if isKeyword(token) {
			writeColored(&result, token, colorYellow)
			if token == "then" || token == "do" || token == "else" || token == "elif" || token == "!" {
				cmdPos = true
				optionMode = false
			}
		} else {
			if strings.HasPrefix(token, "-") && len(token) > 1 {
				optionMode = true
				writeColored(&result, token, colorCyan)
			} else {
				optionMode = false
				colorPathOrArg(&result, token)
			}
		}

		if idx < len(spans)-1 {
			result.WriteString(string(runes[sp.end:spans[idx+1].start]))
		}
	}

	lastEnd := spans[len(spans)-1].end
	if lastEnd < len(runes) && !(len(spans) > 0 && strings.HasPrefix(string(runes[spans[0].start:spans[0].end]), "#")) {
		result.WriteString(string(runes[lastEnd:]))
	}
	return result.String()
}

const colorInverse = "\x1b[7m"
const colorDim = "\x1b[90m"

func (e *LineEditor) renderMenu(prompt string, prevBufW int) int {
	termW := getTermWidth()
	if termW <= 0 {
		termW = 80
	}

	// clear previous menu if present
	if e.menuRows > 0 {
		var clBuf strings.Builder
		clBuf.WriteString("\033[B\r\033[K")
		for i := 1; i < e.menuRows; i++ {
			clBuf.WriteString("\033[B\r\033[K")
		}
		for i := 0; i < e.menuRows; i++ {
			clBuf.WriteString("\033[A")
		}
		os.Stdout.WriteString(clBuf.String())
		e.menuRows = 0
	}

	maxNameLen := 0
	for _, c := range e.menuCandidates {
		w := visibleWidth(c.name)
		if w > maxNameLen {
			maxNameLen = w
		}
	}
	colWidth := maxNameLen + 2
	cols := 1
	if colWidth <= termW {
		cols = termW / colWidth
	}
	if cols < 1 {
		cols = 1
	}
	rows := (len(e.menuCandidates) + cols - 1) / cols
	maxMenuRows := 10
	if rows > maxMenuRows {
		cols = (len(e.menuCandidates) + maxMenuRows - 1) / maxMenuRows
		if cols < 1 {
			cols = 1
		}
		rows = (len(e.menuCandidates) + cols - 1) / cols
	}

	var buf strings.Builder
	savedScreenRow := e.screenRow
	buf.WriteString("\r\n")
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			idx := row + col*rows
			if idx >= len(e.menuCandidates) {
				break
			}
			entry := e.menuCandidates[idx]
			nameW := visibleWidth(entry.name)

			if idx == e.menuSelected {
				buf.WriteString(colorInverse)
				buf.WriteString(entry.name)
				if col < cols-1 && row+((col+1)*rows) < len(e.menuCandidates) {
					if entry.desc != "" {
						avail := colWidth - nameW
						if avail > 4 {
							buf.WriteString(colorReset)
							buf.WriteString(" ")
							descW := avail - 1
							truncated := truncateVisible(entry.desc, descW)
							buf.WriteString(colorDim)
							buf.WriteString(truncated)
							pad := colWidth - nameW - visibleWidth(truncated) - 1
							buf.WriteString(colorReset)
							buf.WriteString(colorInverse)
							buf.WriteString(strings.Repeat(" ", pad))
						} else {
							buf.WriteString(strings.Repeat(" ", colWidth-nameW))
						}
					} else {
						buf.WriteString(strings.Repeat(" ", colWidth-nameW))
					}
					buf.WriteString(colorReset)
				} else if entry.desc != "" && colWidth > nameW+4 {
					buf.WriteString(colorReset)
					buf.WriteString(" ")
					avail := colWidth - nameW - 1
					buf.WriteString(colorDim)
					buf.WriteString(truncateVisible(entry.desc, avail))
					buf.WriteString(colorReset)
				} else {
					buf.WriteString(colorReset)
				}
			} else {
				buf.WriteString(entry.name)
				if col < cols-1 && row+((col+1)*rows) < len(e.menuCandidates) {
					spaces := colWidth - nameW
					if entry.desc != "" && spaces > 4 {
						buf.WriteString(" ")
						descW := spaces - 1
						buf.WriteString(colorDim)
						buf.WriteString(truncateVisible(entry.desc, descW))
						pad := spaces - 1 - visibleWidth(truncateVisible(entry.desc, descW))
						buf.WriteString(colorReset)
						buf.WriteString(strings.Repeat(" ", pad))
					} else {
						buf.WriteString(strings.Repeat(" ", spaces))
					}
				} else if entry.desc != "" && colWidth > nameW+4 {
					buf.WriteString(" ")
					avail := colWidth - nameW - 1
					buf.WriteString(colorDim)
					buf.WriteString(truncateVisible(entry.desc, avail))
					buf.WriteString(colorReset)
				}
			}
		}
		if row < rows-1 {
			buf.WriteString("\r\n")
		} else {
			buf.WriteString("\033[K")
		}
	}

	for i := 0; i < rows; i++ {
		buf.WriteString("\033[A")
	}

	os.Stdout.WriteString(buf.String())
	e.screenRow = savedScreenRow + 1
	e.menuRows = rows
	return e.redraw(prompt, prevBufW)
}

func truncateVisible(s string, maxW int) string {
	if maxW <= 0 {
		return ""
	}
	runes := []rune(s)
	w := 0
	for i, r := range runes {
		rw := runeWidth(r)
		if w+rw > maxW {
			if i > 0 && maxW > 2 {
				result := string(runes[:i])
				resultW := visibleWidth(result)
				trim := resultW - maxW + 1
				if trim > 0 {
					for trim > 0 {
						r := runes[i-1]
						rw := runeWidth(r)
						i--
						trim -= rw
					}
					result = string(runes[:i])
				}
				return result + "\u2026"
			}
			return string(runes[:i])
		}
		w += rw
	}
	return s
}

func (e *LineEditor) clearMenu(prompt string, prevBufW int) int {
	if e.menuRows <= 0 {
		return prevBufW
	}
	var buf strings.Builder
	buf.WriteString("\033[B\r\033[K")
	for i := 1; i < e.menuRows; i++ {
		buf.WriteString("\033[B\r\033[K")
	}
	for i := 0; i < e.menuRows; i++ {
		buf.WriteString("\033[A")
	}
	os.Stdout.WriteString(buf.String())
	e.menuRows = 0
	return e.redraw(prompt, prevBufW)
}

func (e *LineEditor) acceptMenuSelection(prompt string, prevBufW int) int {
	if e.menuSelected < 0 || e.menuSelected >= len(e.menuCandidates) {
		return e.cancelMenu(prompt, prevBufW)
	}
	completion := e.menuCandidates[e.menuSelected].name

	tokens := tokenize(string(e.buf))
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

	var partial string
	if isFirstToken {
		if len(tokens) > 0 {
			partial = tokens[0]
		}
	} else if tokenIdx >= 0 && tokenIdx < len(tokens) {
		partial = tokens[tokenIdx]
	}

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

	e.menuActive = false
	e.menuCandidates = nil
	e.menuSelected = 0
	e.updateSuggestion()
	return e.clearMenu(prompt, 0)
}

func (e *LineEditor) cancelMenu(prompt string, prevBufW int) int {
	e.menuActive = false
	e.menuCandidates = nil
	e.menuSelected = 0
	e.updateSuggestion()
	return e.clearMenu(prompt, prevBufW)
}

func (e *LineEditor) handleTabCompletion(prompt string, prevBufW int) int {
	text := string(e.buf)
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return prevBufW
	}

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

	// in whitespace after a token — check for flag completion first
	if inSpace && tokenIdx >= 1 {
		prevCmd := ""
		for _, t := range tokens {
			if isBuiltin(t) || isKeyword(t) || isAlias(t) {
				prevCmd = t
				break
			}
			if _, ok := hashLookup(t); ok {
				prevCmd = t
				break
			}
		}
		if prevCmd != "" {
			entry := getDescEntry(prevCmd)
			if entry != nil && len(entry.flagChars) > 0 {
				candidates := getFlagCompletions(prevCmd, "")
				if len(candidates) > 0 {
					if e.config != nil && e.config.CompletionMenu {
						e.menuActive = true
						e.menuCandidates = candidates
						e.menuSelected = 0
						e.menuRows = 0
						return e.renderMenu(prompt, prevBufW)
					}
					os.Stdout.Write([]byte("\r\n"))
					for i, c := range candidates {
						if i > 0 && i%6 == 0 {
							os.Stdout.Write([]byte("\r\n"))
						} else if i > 0 {
							os.Stdout.Write([]byte("  "))
						}
						os.Stdout.Write([]byte(c.name))
					}
					os.Stdout.Write([]byte("\r\n"))
					os.Stdout.Write([]byte(prompt))
					e.screenRow = 0
					return e.redraw(prompt, 0)
				}
			}
		}
		candidates := e.completePath("", len(tokens) > 0 && tokens[0] == "cd")
		if len(candidates) > 0 {
			if e.config != nil && e.config.CompletionMenu {
				e.menuActive = true
				e.menuCandidates = candidates
				e.menuSelected = 0
				e.menuRows = 0
				return e.renderMenu(prompt, prevBufW)
			}
			os.Stdout.Write([]byte("\r\n"))
			for i, c := range candidates {
				if i > 0 && i%6 == 0 {
					os.Stdout.Write([]byte("\r\n"))
				} else if i > 0 {
					os.Stdout.Write([]byte("  "))
				}
				os.Stdout.Write([]byte(c.name))
			}
			os.Stdout.Write([]byte("\r\n"))
			os.Stdout.Write([]byte(prompt))
			e.screenRow = 0
			return e.redraw(prompt, 0)
		}
		return prevBufW
	}

	if inSpace && tokenIdx >= 0 {
		candidates := e.completePath("", len(tokens) > 0 && tokens[0] == "cd")
		if len(candidates) > 0 {
			os.Stdout.Write([]byte("\r\n"))
			for i, c := range candidates {
				if i > 0 && i%6 == 0 {
					os.Stdout.Write([]byte("\r\n"))
				} else if i > 0 {
					os.Stdout.Write([]byte("  "))
				}
				os.Stdout.Write([]byte(c.name))
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

	// check for flag completion on non-first token
	if !isFirstToken && len(partial) > 0 && tokenIdx >= 1 {
		prevCmd := tokens[tokenIdx-1]
		entry := getDescEntry(prevCmd)
		if entry != nil && len(entry.flagChars) > 0 {
			flagTriggered := false
			for _, fc := range entry.flagChars {
				if fc != "" && strings.HasPrefix(partial, fc) {
					flagTriggered = true
					break
				}
			}
			if flagTriggered {
				candidates := getFlagCompletions(prevCmd, partial)
				if len(candidates) > 0 {
					return e.applyCompletion(prompt, prevBufW, candidates, partial, false, false)
				}
				return prevBufW
			}
		}
	}

	var candidates []completionEntry
	if isFirstToken {
		candidates = e.completeCommand(partial)
		if e.config != nil && e.config.AutoCd && len(partial) > 0 {
			seen := make(map[string]bool)
			for _, c := range candidates {
				seen[c.name] = true
			}
			for _, c := range e.completePath(partial, len(tokens) > 0 && tokens[0] == "cd") {
				if !seen[c.name] {
					candidates = append(candidates, c)
				}
			}
			sortCompletionEntries(candidates)
		}
	} else {
		candidates = e.completePath(partial, len(tokens) > 0 && tokens[0] == "cd")
	}

	if len(candidates) == 0 {
		if e.config != nil && e.config.AutoCorrect && len(partial) > 0 {
			fuzzy := FuzzyCompletions(partial, e.config.AutoCorrectThreshold, e.config.AutoCd, len(tokens) > 0 && tokens[0] == "cd")
			if len(fuzzy) > 0 {
				return e.applyCompletion(prompt, prevBufW, fuzzy, partial, isFirstToken, true)
			}
		}
		return prevBufW
	}

	return e.applyCompletion(prompt, prevBufW, candidates, partial, isFirstToken, false)
}

func (e *LineEditor) applyCompletion(prompt string, prevBufW int, candidates []completionEntry, partial string, isFirstToken bool, fuzzy bool) int {
	names := make([]string, len(candidates))
	for i, c := range candidates {
		names[i] = c.name
	}

	common := names[0]
	if !fuzzy {
		for _, c := range names[1:] {
			cRunes := []rune(c)
			commonRunes := []rune(common)
			for len(commonRunes) > 0 && !strings.EqualFold(string(cRunes[:min(len(commonRunes), len(cRunes))]), string(commonRunes[:min(len(commonRunes), len(cRunes))])) {
				commonRunes = commonRunes[:len(commonRunes)-1]
			}
			common = string(commonRunes)
		}
	}

	if len(candidates) == 1 {
		completion := candidates[0].name
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

	if !fuzzy {
	if common != partial {
		var commonActual string
		commonRunes := []rune(common)
		for _, c := range candidates {
			cRunes := []rune(c.name)
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
	}

	// Multiple candidates — fish-style cycling
	cycleNames := make([]string, len(candidates))
	for i, c := range candidates {
		cycleNames[i] = c.name
	}
	if e.cycleCandidates != nil && string(e.buf) == e.cycleLastBuf {
		for i := 0; i < e.cycleLen && e.cursor > 0; i++ {
			e.buf = append(e.buf[:e.cursor-1], e.buf[e.cursor:]...)
			e.cursor--
		}
		e.cycleIndex = (e.cycleIndex + 1) % len(e.cycleCandidates)
	} else {
		e.cycleCandidates = cycleNames
		e.cycleIndex = 0
		partialRunes := utf8.RuneCountInString(partial)
		for i := 0; i < partialRunes && e.cursor > 0; i++ {
			e.buf = append(e.buf[:e.cursor-1], e.buf[e.cursor:]...)
			e.cursor--
		}
	}

	for _, r := range e.cycleCandidates[e.cycleIndex] {
		e.buf = append(e.buf[:e.cursor], append([]rune{r}, e.buf[e.cursor:]...)...)
		e.cursor++
	}
	e.cycleLen = utf8.RuneCountInString(e.cycleCandidates[e.cycleIndex])
	e.cycleLastBuf = string(e.buf)

	if e.config != nil && e.config.CompletionMenu {
		e.menuActive = true
		e.menuCandidates = candidates
		e.menuSelected = 0
		e.menuRows = 0
		return e.renderMenu(prompt, prevBufW)
	}
	return e.redraw(prompt, prevBufW)
}

func (e *LineEditor) completeCommand(partial string) []completionEntry {
	var matches []completionEntry

	for _, cmd := range allBuiltins {
		if strings.HasPrefix(cmd, partial) {
			matches = append(matches, completionEntry{name: cmd, desc: getDesc(cmd)})
		}
	}

	for _, kw := range allKeywords {
		if strings.HasPrefix(kw, partial) {
			matches = append(matches, completionEntry{name: kw, desc: getDesc(kw)})
		}
	}

	for _, name := range allAliasNames() {
		if strings.HasPrefix(name, partial) {
			aliasMu.RLock()
			if a, ok := aliasTable[name]; ok {
				matches = append(matches, completionEntry{name: name, desc: a.Raw})
			} else {
				matches = append(matches, completionEntry{name: name, desc: getDesc(name)})
			}
			aliasMu.RUnlock()
		}
	}

	hashScanPath()
	hashMu.RLock()
	seen := make(map[string]bool)
	for name := range hashTable {
		if seen[name] {
			continue
		}
		seen[name] = true
		if strings.HasPrefix(name, partial) {
			matches = append(matches, completionEntry{name: name, desc: getDesc(name)})
		}
	}
	hashMu.RUnlock()

	sortCompletionEntries(matches)
	return matches
}

func (e *LineEditor) completePath(partial string, dirsOnly bool) []completionEntry {
	dir := "."
	prefix := partial

	if strings.HasPrefix(partial, "~/") {
		home := os.Getenv("HOME")
		if home != "" {
			dir = home
			prefix = partial[2:]
		}
	} else if partial == "~" {
		home := os.Getenv("HOME")
		if home != "" {
			return []completionEntry{{name: "~/"}}
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

	var matches []completionEntry
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

		var meta string
		isDir := info.IsDir()
		mode := info.Mode()
		if isDir {
			name += "/"
			meta = "(dir)"
		} else if mode&0111 != 0 {
			meta = "(exe)"
		}
		if mode&os.ModeSymlink != 0 {
			if meta != "" {
				meta = "(sym)"
			} else {
				meta = "(sym)"
			}
		}

		if dirsOnly && !isDir {
			continue
		}

		if strings.Contains(partial, "/") {
			idx := strings.LastIndex(partial, "/")
			name = partial[:idx+1] + name
		} else if strings.HasPrefix(partial, "~/") {
			name = "~/" + name
		} else if partial == "~" {
			name = "~/" + name
		}

		matches = append(matches, completionEntry{name: name, desc: meta})
	}

	sortCompletionEntries(matches)
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
