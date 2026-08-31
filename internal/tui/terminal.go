package tui

import (
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/mattn/go-runewidth"
)

type terminalColor struct {
	mode uint8 // 0 default, 1 indexed, 2 RGB
	a    uint8
	b    uint8
	c    uint8
}

type terminalStyle struct {
	fg, bg                         terminalColor
	bold, faint, italic, underline bool
	blink, inverse, strike         bool
}

type terminalCell struct {
	rune         rune
	style        terminalStyle
	continuation bool
}

type terminalSnapshot struct {
	rows                    [][]terminalCell
	cx, cy, savedX, savedY  int
	scrollTop, scrollBottom int
}

// terminalScreen is an in-memory VT/xterm screen. Bubble Tea owns the real
// terminal, so SSH output is interpreted here and rendered as styled cells.
type terminalScreen struct {
	w, h              int
	cx, cy            int
	savedX, savedY    int
	rows              [][]terminalCell
	scrollTop         int
	scrollBottom      int
	state             int
	csi               strings.Builder
	osc               strings.Builder
	utf8Buffer        []byte
	style             terminalStyle
	wrap              bool
	originMode        bool
	applicationCursor bool
	cursorVisible     bool
	g0Graphics        bool
	g1Graphics        bool
	useG1             bool
	charsetTarget     byte
	altActive         bool
	bracketedPaste    bool
	primary           *terminalSnapshot
	responses         [][]byte
	currentDir        string
}

func newTerminalScreen(w, h int) *terminalScreen {
	t := &terminalScreen{wrap: true, cursorVisible: true}
	t.Resize(w, h)
	return t
}

func (t *terminalScreen) Resize(w, h int) {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	t.rows, t.cx, t.cy = resizeCells(t.rows, w, h, t.cx, t.cy)
	if t.primary != nil {
		t.primary.rows, t.primary.cx, t.primary.cy = resizeCells(t.primary.rows, w, h, t.primary.cx, t.primary.cy)
		t.primary.scrollTop, t.primary.scrollBottom = 0, h-1
	}
	t.w, t.h = w, h
	t.scrollTop, t.scrollBottom = 0, h-1
	t.cx, t.cy = clamp(t.cx, 0, w-1), clamp(t.cy, 0, h-1)
}

func resizeCells(old [][]terminalCell, w, h, cx, cy int) ([][]terminalCell, int, int) {
	rows := make([][]terminalCell, h)
	for y := range rows {
		rows[y] = blankCellRow(w, terminalStyle{})
	}
	startOld, startNew, copyH := 0, 0, len(old)
	if copyH > h {
		startOld, copyH = copyH-h, h
		cy -= startOld
	} else if h > copyH {
		startNew = h - copyH
		if copyH > 0 {
			cy += startNew
		}
	}
	for y := 0; y < copyH; y++ {
		copy(rows[startNew+y], old[startOld+y])
	}
	return rows, clamp(cx, 0, w-1), clamp(cy, 0, h-1)
}

func blankCellRow(w int, style terminalStyle) []terminalCell {
	row := make([]terminalCell, w)
	for i := range row {
		row[i] = terminalCell{rune: ' ', style: style}
	}
	return row
}

func (t *terminalScreen) Feed(data []byte) {
	if len(t.utf8Buffer) > 0 {
		data = append(append([]byte(nil), t.utf8Buffer...), data...)
		t.utf8Buffer = nil
	}
	for len(data) > 0 {
		if t.state != 0 || data[0] < utf8.RuneSelf {
			t.controlByte(data[0])
			data = data[1:]
			continue
		}
		r, n := utf8.DecodeRune(data)
		if r == utf8.RuneError && n == 1 && !utf8.FullRune(data) {
			t.utf8Buffer = append(t.utf8Buffer, data...)
			return
		}
		t.put(r)
		data = data[n:]
	}
}

func (t *terminalScreen) controlByte(b byte) {
	switch t.state {
	case 0:
		switch b {
		case 0x1b:
			t.state = 1
		case '\r':
			t.cx = 0
		case '\n', 0x0b, 0x0c:
			t.lineFeed()
		case '\b':
			if t.cx > 0 {
				t.cx--
			}
		case '\t':
			t.cx = min(((t.cx/8)+1)*8, t.w-1)
		case 0x0e:
			t.useG1 = true
		case 0x0f:
			t.useG1 = false
		default:
			if b >= 0x20 && b != 0x7f {
				t.put(rune(b))
			}
		}
	case 1:
		switch b {
		case '[':
			t.state = 2
			t.csi.Reset()
		case ']':
			t.osc.Reset()
			t.state = 3
		case '(':
			t.charsetTarget = 0
			t.state = 5
		case ')':
			t.charsetTarget = 1
			t.state = 5
		case 'P', '_', '^', 'X':
			t.state = 6
		case '7':
			t.savedX, t.savedY, t.state = t.cx, t.cy, 0
		case '8':
			t.cx, t.cy, t.state = t.savedX, t.savedY, 0
		case 'D':
			t.lineFeed()
			t.state = 0
		case 'E':
			t.cx = 0
			t.lineFeed()
			t.state = 0
		case 'M':
			t.reverseIndex()
			t.state = 0
		case 'c':
			t.reset()
			t.state = 0
		default:
			t.state = 0
		}
	case 2:
		if b >= 0x40 && b <= 0x7e {
			t.applyCSI(b, t.csi.String())
			t.state = 0
		} else if t.csi.Len() < 256 {
			t.csi.WriteByte(b)
		} else {
			t.state = 0
		}
	case 3: // OSC: capture the current-directory notification; ignore others.
		if b == 0x07 {
			t.applyOSC(t.osc.String())
			t.state = 0
		} else if b == 0x1b {
			t.state = 4
		} else if t.osc.Len() < 4096 {
			t.osc.WriteByte(b)
		}
	case 4:
		if b == '\\' {
			t.applyOSC(t.osc.String())
			t.state = 0
		} else {
			if t.osc.Len() < 4095 {
				t.osc.WriteByte(0x1b)
				t.osc.WriteByte(b)
			}
			t.state = 3
		}
	case 5:
		if t.charsetTarget == 0 {
			t.g0Graphics = b == '0'
		} else {
			t.g1Graphics = b == '0'
		}
		t.state = 0
	case 6: // DCS/APC/PM/SOS: ignore payload until ST.
		if b == 0x1b {
			t.state = 7
		}
	case 7:
		if b == '\\' {
			t.state = 0
		} else {
			t.state = 6
		}
	}
}

// applyOSC handles OSC 7, the standard shell notification containing the
// current working directory. Bash/zsh/fish integrations and many prompts emit
// it, which lets drag-and-drop uploads follow `cd` without modifying the shell.
func (t *terminalScreen) applyOSC(payload string) {
	if !strings.HasPrefix(payload, "7;") {
		return
	}
	raw := strings.TrimPrefix(payload, "7;")
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return
	}
	directory, err := url.PathUnescape(u.EscapedPath())
	if err != nil || !path.IsAbs(directory) {
		return
	}
	t.currentDir = path.Clean(directory)
}

func (t *terminalScreen) SetCurrentDirectory(directory string) {
	if path.IsAbs(directory) {
		t.currentDir = path.Clean(directory)
	}
}

func (t *terminalScreen) CurrentDirectory() string { return t.currentDir }

func (t *terminalScreen) PasteBytes(content string) []byte {
	if t.bracketedPaste {
		return []byte("\x1b[200~" + content + "\x1b[201~")
	}
	return []byte(content)
}

// KeyBytes encodes cursor keys according to DECCKM. Full-screen programs such
// as htop and mc enable application cursor mode with CSI ? 1 h and expect SS3
// sequences (ESC O A/B/C/D) until they disable it again.
func (t *terminalScreen) KeyBytes(key tea.KeyPressMsg) []byte {
	return keyBytesForMode(key, t.applicationCursor)
}

func (t *terminalScreen) put(r rune) {
	if (t.useG1 && t.g1Graphics) || (!t.useG1 && t.g0Graphics) {
		r = decSpecialRune(r)
	}
	width := runewidth.RuneWidth(r)
	if width <= 0 {
		return
	}
	if t.cx >= t.w {
		if !t.wrap {
			t.cx = t.w - 1
		} else {
			t.cx = 0
			t.lineFeed()
		}
	}
	if t.rows[t.cy][t.cx].continuation && t.cx > 0 {
		t.rows[t.cy][t.cx-1] = terminalCell{rune: ' ', style: t.style}
	}
	if t.cx+1 < t.w && t.rows[t.cy][t.cx+1].continuation {
		t.rows[t.cy][t.cx+1] = terminalCell{rune: ' ', style: t.style}
	}
	t.rows[t.cy][t.cx] = terminalCell{rune: r, style: t.style}
	if width == 2 && t.cx+1 < t.w {
		t.rows[t.cy][t.cx+1] = terminalCell{rune: ' ', style: t.style, continuation: true}
	}
	t.cx += width
}

func decSpecialRune(r rune) rune {
	if replacement, ok := map[rune]rune{
		'j': '┘', 'k': '┐', 'l': '┌', 'm': '└', 'n': '┼', 'q': '─',
		't': '├', 'u': '┤', 'v': '┴', 'w': '┬', 'x': '│', '`': '◆',
		'a': '▒', 'f': '°', 'g': '±', 'y': '≤', 'z': '≥', '{': 'π',
	}[r]; ok {
		return replacement
	}
	return r
}

func (t *terminalScreen) lineFeed() {
	if t.cy == t.scrollBottom {
		t.scrollUp(t.scrollTop, t.scrollBottom, 1)
	} else if t.cy < t.h-1 {
		t.cy++
	}
}

func (t *terminalScreen) reverseIndex() {
	if t.cy == t.scrollTop {
		t.scrollDown(t.scrollTop, t.scrollBottom, 1)
	} else if t.cy > 0 {
		t.cy--
	}
}

func (t *terminalScreen) scrollUp(top, bottom, count int) {
	if top < 0 || bottom >= t.h || top > bottom {
		return
	}
	count = min(max(1, count), bottom-top+1)
	copy(t.rows[top:bottom-count+1], t.rows[top+count:bottom+1])
	for y := bottom - count + 1; y <= bottom; y++ {
		t.rows[y] = blankCellRow(t.w, t.style)
	}
}

func (t *terminalScreen) scrollDown(top, bottom, count int) {
	if top < 0 || bottom >= t.h || top > bottom {
		return
	}
	count = min(max(1, count), bottom-top+1)
	copy(t.rows[top+count:bottom+1], t.rows[top:bottom-count+1])
	for y := top; y < top+count; y++ {
		t.rows[y] = blankCellRow(t.w, t.style)
	}
}

func (t *terminalScreen) applyCSI(final byte, raw string) {
	private := strings.HasPrefix(raw, "?")
	clean := strings.TrimLeft(raw, "?<>=!")
	parts := strings.Split(clean, ";")
	if final == 'm' && strings.Contains(clean, ":") {
		parts = strings.FieldsFunc(clean, func(r rune) bool { return r == ';' || r == ':' })
	}
	args := make([]int, len(parts))
	for i, part := range parts {
		if part != "" {
			args[i], _ = strconv.Atoi(part)
		}
	}
	arg := func(i, fallback int) int {
		if i >= len(args) || args[i] == 0 {
			return fallback
		}
		return args[i]
	}
	topOffset := 0
	if t.originMode {
		topOffset = t.scrollTop
	}
	switch final {
	case 'A':
		t.cy = max(topOffset, t.cy-arg(0, 1))
	case 'B', 'e':
		t.cy = min(t.scrollBottomIfOrigin(), t.cy+arg(0, 1))
	case 'C', 'a':
		t.cx = min(t.w-1, t.cx+arg(0, 1))
	case 'D':
		t.cx = max(0, t.cx-arg(0, 1))
	case 'E':
		t.cy, t.cx = min(t.scrollBottomIfOrigin(), t.cy+arg(0, 1)), 0
	case 'F':
		t.cy, t.cx = max(topOffset, t.cy-arg(0, 1)), 0
	case 'G', '`':
		t.cx = clamp(arg(0, 1)-1, 0, t.w-1)
	case 'H', 'f':
		t.cy = clamp(topOffset+arg(0, 1)-1, topOffset, t.scrollBottomIfOrigin())
		t.cx = clamp(arg(1, 1)-1, 0, t.w-1)
	case 'd':
		t.cy = clamp(topOffset+arg(0, 1)-1, topOffset, t.scrollBottomIfOrigin())
	case 'J':
		t.eraseDisplay(arg(0, 0))
	case 'K':
		t.eraseInLine(arg(0, 0))
	case 'X':
		t.eraseLine(t.cy, t.cx, min(t.w, t.cx+arg(0, 1)))
	case 'P':
		n := min(arg(0, 1), t.w-t.cx)
		copy(t.rows[t.cy][t.cx:], t.rows[t.cy][t.cx+n:])
		t.eraseLine(t.cy, t.w-n, t.w)
	case '@':
		n := min(arg(0, 1), t.w-t.cx)
		copy(t.rows[t.cy][t.cx+n:], t.rows[t.cy][t.cx:t.w-n])
		t.eraseLine(t.cy, t.cx, t.cx+n)
	case 'L':
		if t.cy >= t.scrollTop && t.cy <= t.scrollBottom {
			t.scrollDown(t.cy, t.scrollBottom, arg(0, 1))
		}
	case 'M':
		if t.cy >= t.scrollTop && t.cy <= t.scrollBottom {
			t.scrollUp(t.cy, t.scrollBottom, arg(0, 1))
		}
	case 'S':
		t.scrollUp(t.scrollTop, t.scrollBottom, arg(0, 1))
	case 'T':
		t.scrollDown(t.scrollTop, t.scrollBottom, arg(0, 1))
	case 'r':
		top, bottom := arg(0, 1)-1, arg(1, t.h)-1
		if top >= 0 && bottom < t.h && top < bottom {
			t.scrollTop, t.scrollBottom = top, bottom
			t.cx, t.cy = 0, topOffset
		}
	case 'm':
		t.applySGR(args)
	case 'n':
		switch arg(0, 0) {
		case 5:
			t.responses = append(t.responses, []byte("\x1b[0n"))
		case 6:
			t.responses = append(t.responses, []byte("\x1b["+strconv.Itoa(t.cy+1)+";"+strconv.Itoa(t.cx+1)+"R"))
		}
	case 'c':
		if !strings.HasPrefix(raw, ">") {
			t.responses = append(t.responses, []byte("\x1b[?1;2c"))
		}
	case 's':
		t.savedX, t.savedY = t.cx, t.cy
	case 'u':
		t.cx, t.cy = t.savedX, t.savedY
	case 'h', 'l':
		t.setMode(private, args, final == 'h')
	}
}

func (t *terminalScreen) scrollBottomIfOrigin() int {
	if t.originMode {
		return t.scrollBottom
	}
	return t.h - 1
}

func (t *terminalScreen) eraseDisplay(mode int) {
	switch mode {
	case 0:
		t.eraseLine(t.cy, t.cx, t.w)
		for y := t.cy + 1; y < t.h; y++ {
			t.eraseLine(y, 0, t.w)
		}
	case 1:
		for y := 0; y < t.cy; y++ {
			t.eraseLine(y, 0, t.w)
		}
		t.eraseLine(t.cy, 0, t.cx+1)
	case 2, 3:
		t.clearAll()
	}
}

func (t *terminalScreen) eraseInLine(mode int) {
	switch mode {
	case 0:
		t.eraseLine(t.cy, t.cx, t.w)
	case 1:
		t.eraseLine(t.cy, 0, t.cx+1)
	case 2:
		t.eraseLine(t.cy, 0, t.w)
	}
}

func (t *terminalScreen) setMode(private bool, args []int, enabled bool) {
	if !private {
		return
	}
	for _, mode := range args {
		switch mode {
		case 1:
			t.applicationCursor = enabled
		case 6:
			t.originMode = enabled
			t.cx = 0
			if enabled {
				t.cy = t.scrollTop
			} else {
				t.cy = 0
			}
		case 7:
			t.wrap = enabled
		case 25:
			t.cursorVisible = enabled
		case 47, 1047, 1049:
			if enabled {
				t.enterAltScreen()
			} else {
				t.exitAltScreen()
			}
		case 2004:
			t.bracketedPaste = enabled
		}
	}
}

func (t *terminalScreen) enterAltScreen() {
	if t.altActive {
		return
	}
	t.primary = &terminalSnapshot{
		rows: t.rows, cx: t.cx, cy: t.cy, savedX: t.savedX, savedY: t.savedY,
		scrollTop: t.scrollTop, scrollBottom: t.scrollBottom,
	}
	t.rows = make([][]terminalCell, t.h)
	for y := range t.rows {
		t.rows[y] = blankCellRow(t.w, terminalStyle{})
	}
	t.cx, t.cy, t.savedX, t.savedY = 0, 0, 0, 0
	t.scrollTop, t.scrollBottom, t.altActive = 0, t.h-1, true
}

func (t *terminalScreen) exitAltScreen() {
	if !t.altActive || t.primary == nil {
		return
	}
	t.rows, t.cx, t.cy = t.primary.rows, t.primary.cx, t.primary.cy
	t.savedX, t.savedY = t.primary.savedX, t.primary.savedY
	t.scrollTop, t.scrollBottom = t.primary.scrollTop, t.primary.scrollBottom
	t.primary, t.altActive = nil, false
}

func (t *terminalScreen) applySGR(args []int) {
	if len(args) == 0 {
		args = []int{0}
	}
	for i := 0; i < len(args); i++ {
		code := args[i]
		switch {
		case code == 0:
			t.style = terminalStyle{}
		case code == 1:
			t.style.bold = true
		case code == 2:
			t.style.faint = true
		case code == 3:
			t.style.italic = true
		case code == 4 || code == 21:
			t.style.underline = true
		case code == 5 || code == 6:
			t.style.blink = true
		case code == 7:
			t.style.inverse = true
		case code == 9:
			t.style.strike = true
		case code == 22:
			t.style.bold, t.style.faint = false, false
		case code == 23:
			t.style.italic = false
		case code == 24:
			t.style.underline = false
		case code == 25:
			t.style.blink = false
		case code == 27:
			t.style.inverse = false
		case code == 29:
			t.style.strike = false
		case code >= 30 && code <= 37:
			t.style.fg = indexedColor(code - 30)
		case code == 38:
			i = applyExtendedColor(args, i, &t.style.fg)
		case code == 39:
			t.style.fg = terminalColor{}
		case code >= 40 && code <= 47:
			t.style.bg = indexedColor(code - 40)
		case code == 48:
			i = applyExtendedColor(args, i, &t.style.bg)
		case code == 49:
			t.style.bg = terminalColor{}
		case code >= 90 && code <= 97:
			t.style.fg = indexedColor(code - 90 + 8)
		case code >= 100 && code <= 107:
			t.style.bg = indexedColor(code - 100 + 8)
		}
	}
}

func indexedColor(index int) terminalColor {
	return terminalColor{mode: 1, a: uint8(clamp(index, 0, 255))}
}

func applyExtendedColor(args []int, i int, color *terminalColor) int {
	if i+2 < len(args) && args[i+1] == 5 {
		*color = indexedColor(args[i+2])
		return i + 2
	}
	if i+4 < len(args) && args[i+1] == 2 {
		*color = terminalColor{mode: 2, a: uint8(clamp(args[i+2], 0, 255)), b: uint8(clamp(args[i+3], 0, 255)), c: uint8(clamp(args[i+4], 0, 255))}
		return i + 4
	}
	return i
}

func (t *terminalScreen) eraseLine(y, from, to int) {
	if y < 0 || y >= t.h {
		return
	}
	from, to = clamp(from, 0, t.w), clamp(to, 0, t.w)
	for x := from; x < to; x++ {
		t.rows[y][x] = terminalCell{rune: ' ', style: t.style}
	}
}

func (t *terminalScreen) clearAll() {
	for y := range t.rows {
		t.eraseLine(y, 0, t.w)
	}
}

func (t *terminalScreen) reset() {
	t.style = terminalStyle{}
	t.wrap, t.originMode, t.applicationCursor, t.cursorVisible = true, false, false, true
	t.g0Graphics, t.g1Graphics, t.useG1 = false, false, false
	t.scrollTop, t.scrollBottom = 0, t.h-1
	t.cx, t.cy, t.savedX, t.savedY = 0, 0, 0, 0
	t.clearAll()
}

func (t *terminalScreen) String() string {
	lines := make([]string, len(t.rows))
	for y, row := range t.rows {
		var line strings.Builder
		for _, cell := range row {
			if !cell.continuation {
				line.WriteRune(cell.rune)
			}
		}
		lines[y] = strings.TrimRight(line.String(), " ")
	}
	return strings.Join(lines, "\n")
}

func (t *terminalScreen) RenderANSI() string {
	var out strings.Builder
	active := terminalStyle{}
	styled := false
	for y, row := range t.rows {
		for x, cell := range row {
			if cell.continuation {
				continue
			}
			style := cell.style
			if t.cursorVisible && y == t.cy && x == min(t.cx, t.w-1) {
				style.inverse = !style.inverse
			}
			if !styled || style != active {
				out.WriteString(styleANSI(style))
				active, styled = style, true
			}
			out.WriteRune(cell.rune)
		}
		out.WriteString("\x1b[0m")
		styled, active = false, terminalStyle{}
		if y+1 < len(t.rows) {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func (t *terminalScreen) DrainResponses() [][]byte {
	responses := t.responses
	t.responses = nil
	return responses
}

func styleANSI(style terminalStyle) string {
	codes := []string{"0"}
	if style.bold {
		codes = append(codes, "1")
	}
	if style.faint {
		codes = append(codes, "2")
	}
	if style.italic {
		codes = append(codes, "3")
	}
	if style.underline {
		codes = append(codes, "4")
	}
	if style.blink {
		codes = append(codes, "5")
	}
	if style.inverse {
		codes = append(codes, "7")
	}
	if style.strike {
		codes = append(codes, "9")
	}
	codes = appendColorCode(codes, style.fg, false)
	codes = appendColorCode(codes, style.bg, true)
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

func appendColorCode(codes []string, color terminalColor, background bool) []string {
	base := 38
	if background {
		base = 48
	}
	switch color.mode {
	case 1:
		return append(codes, strconv.Itoa(base), "5", strconv.Itoa(int(color.a)))
	case 2:
		return append(codes, strconv.Itoa(base), "2", strconv.Itoa(int(color.a)), strconv.Itoa(int(color.b)), strconv.Itoa(int(color.c)))
	default:
		return codes
	}
}

func clamp(value, low, high int) int {
	return min(max(value, low), high)
}
