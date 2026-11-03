package tui

import (
	"fmt"
	"strings"
	"testing"
)

func benchmarkOutput() []byte {
	var b strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&b, "\x1b[32m%05d\x1b[0m \x1b[1;34mINFO\x1b[0m request handled path=/api/v1/items/%d status=200 ┼─┤ ✓\r\n", i, i)
	}
	return []byte(b.String())
}

// BenchmarkTerminalFeed measures parsing a log-like stream once the scrollback
// is already full, which is the steady state of a long `tail -f` session.
func BenchmarkTerminalFeed(b *testing.B) {
	data := benchmarkOutput()
	screen := newTerminalScreen(160, 48)
	for screen.history.Len() < terminalHistoryLimit {
		screen.Feed(data)
	}
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		screen.Feed(data)
	}
}

func BenchmarkTerminalRender(b *testing.B) {
	screen := newTerminalScreen(160, 48)
	screen.Feed(benchmarkOutput())
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = screen.RenderANSI()
	}
}

func BenchmarkTerminalResize(b *testing.B) {
	screen := newTerminalScreen(160, 48)
	for screen.history.Len() < terminalHistoryLimit {
		screen.Feed(benchmarkOutput())
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		screen.Resize(150+i%20, 48)
	}
}
