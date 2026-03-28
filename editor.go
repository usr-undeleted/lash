package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/term"
)

type LineEditor struct {
	buf     []rune
	cursor  int
	history []string
	histIdx int
	config  *Config
}

func NewLineEditor(cfg *Config) *LineEditor {
	return &LineEditor{config: cfg}
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
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
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
	esc := false
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\x1b' {
			esc = true
			continue
		}
		if esc {
			if runes[i] == 'm' {
				esc = false
			}
			continue
		}
		if runes[i] == '\n' {
			w = 0
		} else {
			w += runeWidth(runes[i])
		}
	}
	return w
}

func runeWidth(r rune) int {
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
				e.history = append(e.history, line)
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
			prevW = e.redraw(prompt, 0)

		default:
			if b[0] >= 32 {
				e.buf = append(e.buf[:e.cursor], append([]rune{rune(b[0])}, e.buf[e.cursor:]...)...)
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

func (e *LineEditor) handleEscape(prompt string, prevW *int) {
	b2 := e.readByte()
	if b2 == '[' {
		e.handleCSI(prompt, prevW)
	} else if b2 == 'O' {
		e.handleSS3(prompt, prevW)
	} else if b2 == '\x7f' || b2 == 8 {
		e.deleteWordBack(prompt, prevW)
	}
}

func (e *LineEditor) handleCSI(prompt string, prevW *int) {
	var params []int
	var current int
	for {
		b := e.readByte()
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
						os.Stdout.Write([]byte("\x1b[C"))
					}
				}
			case 'D':
				if len(params) >= 2 && params[1] == 5 {
					e.moveWordBack(prompt, prevW)
				} else {
					if e.cursor > 0 {
						e.cursor--
						os.Stdout.Write([]byte("\x1b[D"))
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
						if e.cursor < len(e.buf) {
							e.buf = append(e.buf[:e.cursor], e.buf[e.cursor+1:]...)
							*prevW = e.redraw(prompt, *prevW)
						}
					case 4:
						e.cursor = len(e.buf)
						*prevW = e.redraw(prompt, *prevW)
					}
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
			os.Stdout.Write([]byte("\x1b[C"))
		}
	case 'D':
		if e.cursor > 0 {
			e.cursor--
			os.Stdout.Write([]byte("\x1b[D"))
		}
	case 'H':
		e.cursor = 0
		*prevW = e.redraw(prompt, *prevW)
	case 'F':
		e.cursor = len(e.buf)
		*prevW = e.redraw(prompt, *prevW)
	}
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

func (e *LineEditor) redraw(prompt string, prevBufW int) int {
	pvis := visibleWidth(prompt)

	termW := getTermWidth()
	if termW <= 0 {
		termW = 80
	}

	prevRows := (pvis + prevBufW + termW - 1) / termW
	if prevRows > 1 {
		fmt.Printf("\033[%dA", prevRows-1)
	}

	fmt.Print("\r")
	fmt.Printf("\033[%dC", pvis)
	fmt.Print("\033[J")

	if e.config != nil && e.config.SyntaxColor && len(e.buf) > 0 {
		os.Stdout.Write([]byte(e.syntaxHighlight()))
	} else {
		os.Stdout.Write([]byte(string(e.buf)))
	}

	newW := bufWidth(e.buf)
	cursorW := bufWidth(e.buf[:e.cursor])
	back := newW - cursorW
	if back > 0 {
		fmt.Printf("\033[%dD", back)
	}

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

	if isValidCommand(tokens[0]) {
		return colorGreen + cmdPart + colorReset + rest
	}
	return colorRed + cmdPart + colorReset + rest
}
