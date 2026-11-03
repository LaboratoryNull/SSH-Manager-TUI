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

type terminalPoint struct {
	x, y int
}

type terminalSelection struct {
	anchor, head terminalPoint
	dragging     bool
	moved        bool
	text         string
}

func (s *terminalSelection) bounds() (terminalPoint, terminalPoint) {
	if s == nil {
		return terminalPoint{}, terminalPoint{}
	}
	if s.anchor.y < s.head.y || (s.anchor.y == s.head.y && s.anchor.x <= s.head.x) {
		return s.anchor, s.head
	}
	return s.head, s.anchor
}

func (s *terminalSelection) hasRange() bool {
	return s != nil && (s.moved || s.anchor != s.head)
}

func (s *terminalSelection) contains(x, y int) bool {
	if !s.hasRange() {
		return false
	}
	start, end := s.bounds()
	if y < start.y || y > end.y {
		return false
	}
	if start.y == end.y {
		return x >= start.x && x <= end.x
	}
	if y == start.y {
		return x >= start.x
	}
	if y == end.y {
		return x <= end.x
	}
	return true
}

// terminalScreen is an in-memory VT/xterm screen. Bubble Tea owns the real
// terminal, so SSH output is interpreted here and rendered as styled cells.
type terminalScreen struct {
	w, h              int
	cx, cy            int
	savedX, savedY    int
	rows              [][]terminalCell
	history           rowRing
	scratch           [][]terminalCell
	scrollOffset      int
	scrollTop         int
	scrollBottom      int
	state             int
	csi               []byte
	csiArgs           []int
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
	// lastGraphic is the last printed character, repeated by CSI b (REP).
	lastGraphic rune
}

const terminalHistoryLimit = 10_000

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
	if !t.altActive && len(t.rows) > h {
		t.appendHistory(t.rows[:len(t.rows)-h])
	} else if t.altActive && t.primary != nil && len(t.primary.rows) > h {
		t.appendHistory(t.primary.rows[:len(t.primary.rows)-h])
	}
	t.rows, t.cx, t.cy = resizeCells(t.rows, w, h, t.cx, t.cy)
	if t.primary != nil {
		t.primary.rows, t.primary.cx, t.primary.cy = resizeCells(t.primary.rows, w, h, t.primary.cx, t.primary.cy)
		t.primary.scrollTop, t.primary.scrollBottom = 0, h-1
	}
	t.w, t.h = w, h
	t.scrollTop, t.scrollBottom = 0, h-1
	t.cx, t.cy = clamp(t.cx, 0, w-1), clamp(t.cy, 0, h-1)
	t.scrollOffset = clamp(t.scrollOffset, 0, t.history.Len())
}

func resizeCellRow(old []terminalCell, width int) []terminalCell {
	row := blankCellRow(width, terminalStyle{})
	copy(row, old)
	return row
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
	return fillBlank(make([]terminalCell, w), style)
}

// reuseBlankRow turns a row that left the screen into a blank row of width w,
// reusing its backing array when it is large enough.
func reuseBlankRow(row []terminalCell, w int, style terminalStyle) []terminalCell {
	if cap(row) < w {
		return blankCellRow(w, style)
	}
	return fillBlank(row[:w], style)
}

func fillBlank(row []terminalCell, style terminalStyle) []terminalCell {
	if len(row) == 0 {
		return row
	}
	// Doubling copies compile to memmove and are several times faster than
	// assigning the (fairly large) cell struct one element at a time.
	row[0] = terminalCell{rune: ' ', style: style}
	for filled := 1; filled < len(row); filled *= 2 {
		copy(row[filled:], row[:filled])
	}
	return row
}

// rowRing is the bounded scrollback. Once full, pushing a row evicts the
// oldest one in O(1) instead of copying the whole history.
type rowRing struct {
	rows  [][]terminalCell
	start int
}

func (r *rowRing) Len() int { return len(r.rows) }

func (r *rowRing) At(i int) []terminalCell { return r.rows[(r.start+i)%len(r.rows)] }

func (r *rowRing) Clear() { r.rows, r.start = nil, 0 }

// Push stores row without copying it and returns the evicted row, if any, so
// the caller can recycle its memory.
func (r *rowRing) Push(row []terminalCell) []terminalCell {
	if len(r.rows) < terminalHistoryLimit {
		r.rows = append(r.rows, row)
		return nil
	}
	evicted := r.rows[r.start]
	r.rows[r.start] = row
	r.start = (r.start + 1) % len(r.rows)
	return evicted
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
			t.csi = t.csi[:0]
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
			t.applyCSI(b, string(t.csi))
			t.state = 0
		} else if len(t.csi) < 256 {
			t.csi = append(t.csi, b)
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

func (t *terminalScreen) selectionPoint(point terminalPoint) terminalPoint {
	point.x, point.y = clamp(point.x, 0, t.w-1), clamp(point.y, 0, t.h-1)
	if t.visibleRows()[point.y][point.x].continuation && point.x > 0 {
		point.x--
	}
	return point
}

func (t *terminalScreen) SelectionText(selection *terminalSelection) string {
	if selection == nil || !selection.hasRange() {
		return ""
	}
	start, end := selection.bounds()
	start, end = t.selectionPoint(start), t.selectionPoint(end)
	rows := t.visibleRows()
	var lines []string
	for y := start.y; y <= end.y; y++ {
		from, to := 0, t.w-1
		if y == start.y {
			from = start.x
		}
		if y == end.y {
			to = end.x
		}
		var line strings.Builder
		for x := from; x <= to; x++ {
			cell := rows[y][x]
			if !cell.continuation {
				line.WriteRune(cell.rune)
			}
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return strings.Join(lines, "\n")
}

func (t *terminalScreen) put(r rune) {
	t.lastGraphic = r
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

var decSpecialGraphics = map[rune]rune{
	'j': '┘', 'k': '┐', 'l': '┌', 'm': '└', 'n': '┼', 'q': '─',
	't': '├', 'u': '┤', 'v': '┴', 'w': '┬', 'x': '│', '`': '◆',
	'a': '▒', 'f': '°', 'g': '±', 'y': '≤', 'z': '≥', '{': 'π',
}

func decSpecialRune(r rune) rune {
	if replacement, ok := decSpecialGraphics[r]; ok {
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
	toHistory := !t.altActive && top == 0 && bottom == t.h-1
	gone := append(t.scratch[:0], t.rows[top:top+count]...)
	copy(t.rows[top:bottom-count+1], t.rows[top+count:bottom+1])
	for i, row := range gone {
		if toHistory {
			// The row moves into the scrollback as is; the evicted oldest row
			// (if any) is recycled as the new blank line.
			row = t.history.Push(row)
		}
		t.rows[bottom-count+1+i] = reuseBlankRow(row, t.w, t.style)
	}
	if toHistory && t.scrollOffset > 0 {
		t.scrollOffset = min(t.history.Len(), t.scrollOffset+count)
	}
	clear(gone)
	t.scratch = gone[:0]
}

func (t *terminalScreen) appendHistory(rows [][]terminalCell) {
	if len(rows) == 0 {
		return
	}
	for _, row := range rows {
		t.history.Push(append([]terminalCell(nil), row...))
	}
	if t.scrollOffset > 0 {
		t.scrollOffset = min(t.history.Len(), t.scrollOffset+len(rows))
	}
}

func (t *terminalScreen) scrollDown(top, bottom, count int) {
	if top < 0 || bottom >= t.h || top > bottom {
		return
	}
	count = min(max(1, count), bottom-top+1)
	gone := append(t.scratch[:0], t.rows[bottom-count+1:bottom+1]...)
	copy(t.rows[top+count:bottom+1], t.rows[top:bottom-count+1])
	for i, row := range gone {
		t.rows[top+i] = reuseBlankRow(row, t.w, t.style)
	}
	clear(gone)
	t.scratch = gone[:0]
}

func (t *terminalScreen) applyCSI(final byte, raw string) {
	private := strings.HasPrefix(raw, "?")
	clean := strings.TrimLeft(raw, "?<>=!")
	args := parseCSIArgs(t.csiArgs[:0], clean, final == 'm')
	t.csiArgs = args
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
	case 'b':
		// REP: xterm-256color terminfo declares it, and ncurses programs
		// such as nano print one space and repeat it to fill gaps. Without
		// it title bars and the shortcut list collapsed into each other.
		if t.lastGraphic != 0 {
			for range min(arg(0, 1), t.w*t.h) {
				t.put(t.lastGraphic)
			}
		}
	case 'I':
		for range arg(0, 1) {
			t.cx = min(((t.cx/8)+1)*8, t.w-1)
		}
	case 'Z':
		for range arg(0, 1) {
			t.cx = max(0, ((t.cx-1)/8)*8)
		}
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
	case 2:
		t.clearAll()
	case 3:
		t.history.Clear()
		t.scrollOffset = 0
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
	t.scrollOffset = 0
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

// parseCSIArgs splits CSI parameters on ';' without allocating. For SGR the
// ':' sub-parameter separator (e.g. 38:2::R:G:B) is accepted as well, and
// empty sub-parameters are skipped like the previous FieldsFunc split did.
// A parameter that is not a plain number is treated as 0 (default).
func parseCSIArgs(args []int, params string, sgr bool) []int {
	colon := sgr && strings.IndexByte(params, ':') >= 0
	value, valid, empty := 0, true, true
	flush := func() {
		if colon && empty {
			return
		}
		if !valid {
			value = 0
		}
		args = append(args, value)
	}
	for i := 0; i < len(params); i++ {
		c := params[i]
		switch {
		case c == ';' || (colon && c == ':'):
			flush()
			value, valid, empty = 0, true, true
		case c >= '0' && c <= '9':
			if value < 1<<20 {
				value = value*10 + int(c-'0')
			}
			empty = false
		default:
			valid, empty = false, false
		}
	}
	flush()
	return args
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
	t.history.Clear()
	t.scrollOffset = 0
	t.clearAll()
}

func (t *terminalScreen) ScrollLines(lines int) bool {
	if t == nil || t.altActive || lines == 0 {
		return false
	}
	previous := t.scrollOffset
	t.scrollOffset = clamp(t.scrollOffset+lines, 0, t.history.Len())
	return previous != t.scrollOffset
}

func (t *terminalScreen) ScrollToBottom() bool {
	if t == nil || t.scrollOffset == 0 {
		return false
	}
	t.scrollOffset = 0
	return true
}

func (t *terminalScreen) ScrollOffset() int {
	if t == nil {
		return 0
	}
	return t.scrollOffset
}

// endSession returns the emulator to ordinary shell input modes without
// clearing the primary screen or its scrollback. A broken connection may not
// deliver the escape sequences that htop, mc, vim and similar programs use to
// leave the alternate screen and disable application cursor mode.
func (t *terminalScreen) endSession() {
	if t == nil {
		return
	}
	if t.altActive {
		t.exitAltScreen()
	}
	t.state = 0
	t.csi = t.csi[:0]
	t.osc.Reset()
	t.utf8Buffer = nil
	t.style = terminalStyle{}
	t.wrap, t.originMode, t.applicationCursor, t.cursorVisible = true, false, false, true
	t.g0Graphics, t.g1Graphics, t.useG1 = false, false, false
	t.bracketedPaste = false
	t.scrollTop, t.scrollBottom = 0, t.h-1
	t.scrollOffset = 0
	t.responses = nil
}

func (t *terminalScreen) visibleRows() [][]terminalCell {
	if t.scrollOffset == 0 || t.history.Len() == 0 || t.altActive {
		return t.rows
	}
	historyLen := t.history.Len()
	start := historyLen - t.scrollOffset
	visible := make([][]terminalCell, t.h)
	for y := range visible {
		index := start + y
		if index < historyLen {
			// Scrollback keeps the width it was written with; fit only the
			// rows on screen instead of rewriting the whole history on resize.
			row := t.history.At(index)
			if len(row) != t.w {
				row = resizeCellRow(row, t.w)
			}
			visible[y] = row
		} else {
			visible[y] = t.rows[index-historyLen]
		}
	}
	return visible
}

func (t *terminalScreen) String() string {
	rows := t.visibleRows()
	lines := make([]string, len(rows))
	for y, row := range rows {
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
	return t.RenderANSISelection(nil)
}

func (t *terminalScreen) RenderANSISelection(selection *terminalSelection) string {
	var out strings.Builder
	out.Grow(t.h * (t.w + 32))
	active := terminalStyle{}
	styled := false
	rows := t.visibleRows()
	for y, row := range rows {
		for x, cell := range row {
			if cell.continuation {
				continue
			}
			style := cell.style
			selected := selection.contains(x, y)
			if selected {
				style.fg = terminalColor{mode: 2, a: 17, b: 17, c: 27}
				style.bg = terminalColor{mode: 2, a: 125, b: 86, c: 244}
				style.inverse = false
			} else if t.scrollOffset == 0 && t.cursorVisible && y == t.cy && x == min(t.cx, t.w-1) {
				style.inverse = !style.inverse
			}
			if !styled || style != active {
				writeStyleANSI(&out, style)
				active, styled = style, true
			}
			out.WriteRune(cell.rune)
		}
		out.WriteString("\x1b[0m")
		styled, active = false, terminalStyle{}
		if y+1 < len(rows) {
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
	var out strings.Builder
	writeStyleANSI(&out, style)
	return out.String()
}

// writeStyleANSI emits a full SGR reset followed by the cell attributes. It
// writes straight into the frame buffer: rendering runs on every frame, so
// avoiding per-cell slices and strings matters.
func writeStyleANSI(out *strings.Builder, style terminalStyle) {
	out.WriteString("\x1b[0")
	for _, attribute := range [...]struct {
		on   bool
		code string
	}{
		{style.bold, ";1"}, {style.faint, ";2"}, {style.italic, ";3"}, {style.underline, ";4"},
		{style.blink, ";5"}, {style.inverse, ";7"}, {style.strike, ";9"},
	} {
		if attribute.on {
			out.WriteString(attribute.code)
		}
	}
	writeColorCode(out, style.fg, false)
	writeColorCode(out, style.bg, true)
	out.WriteByte('m')
}

func writeColorCode(out *strings.Builder, color terminalColor, background bool) {
	base := ";38"
	if background {
		base = ";48"
	}
	var digits [4]byte
	writeNumber := func(value uint8) {
		out.WriteByte(';')
		out.Write(strconv.AppendUint(digits[:0], uint64(value), 10))
	}
	switch color.mode {
	case 1:
		out.WriteString(base)
		out.WriteString(";5")
		writeNumber(color.a)
	case 2:
		out.WriteString(base)
		out.WriteString(";2")
		writeNumber(color.a)
		writeNumber(color.b)
		writeNumber(color.c)
	}
}

func clamp(value, low, high int) int {
	return min(max(value, low), high)
}
