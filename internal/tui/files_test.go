package tui

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestFlattenTree(t *testing.T) {
	leaf := &fileNode{Name: "file", Path: "/dir/file", Depth: 2}
	dir := &fileNode{Name: "dir", Path: "/dir", Dir: true, Depth: 1, Expanded: true, Children: []*fileNode{leaf}}
	root := &fileNode{Name: "/", Path: "/", Dir: true, Expanded: true, Children: []*fileNode{dir}}
	got := flattenTree(root)
	if len(got) != 3 || got[2] != leaf {
		t.Fatalf("unexpected flattened tree: %#v", got)
	}
	dir.Expanded = false
	if got := flattenTree(root); len(got) != 2 {
		t.Fatalf("collapsed tree length = %d", len(got))
	}
}

func TestLocalSize(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one"), []byte("123"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "nested", "two"), []byte("4567"), 0o600); err != nil {
		t.Fatal(err)
	}
	size, err := localSize(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if size != 7 {
		t.Fatalf("size = %d, want 7", size)
	}
}

func TestLocalSizeRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation requires additional Windows privileges: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := localSize(context.Background(), link); err == nil {
		t.Fatal("expected a symlink error")
	}
}

func TestAvailablePath(t *testing.T) {
	target := filepath.Join(t.TempDir(), "result")
	if got := availablePath(target); got != target {
		t.Fatalf("available path = %q", got)
	}
}

func TestSafeBase(t *testing.T) {
	if got := safeBase("/"); got != "remote-root" {
		t.Fatalf("root base = %q", got)
	}
	if got := safeBase("/tmp/report.txt"); got != "report.txt" {
		t.Fatalf("file base = %q", got)
	}
}

func TestSafeChildName(t *testing.T) {
	if _, err := safeChildName("report.txt"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../secret", "..", "dir/file"} {
		if _, err := safeChildName(name); err == nil {
			t.Fatalf("expected %q to be rejected", name)
		}
	}
}

func TestDroppedLocalPathsRecognizesQuotedEscapedAndURIPaths(t *testing.T) {
	directory := t.TempDir()
	first := filepath.Join(directory, "first file.txt")
	second := filepath.Join(directory, "second")
	if err := os.WriteFile(first, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(second, 0o700); err != nil {
		t.Fatal(err)
	}

	paths, ok := droppedLocalPaths("'" + first + "' " + strings.ReplaceAll(second, " ", "\\ "))
	if !ok || len(paths) != 2 || paths[0] != first || paths[1] != second {
		t.Fatalf("shell paths = %#v, ok=%v", paths, ok)
	}
	uriList := fileDropURL(first) + "\r\n" + fileDropURL(second)
	paths, ok = droppedLocalPaths(uriList)
	if !ok || len(paths) != 2 || paths[0] != first || paths[1] != second {
		t.Fatalf("URI paths = %#v, ok=%v", paths, ok)
	}
}

func TestDroppedLocalPathsRecognizesWindowsPaths(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific drag-and-drop formats")
	}
	directory := t.TempDir()
	first := filepath.Join(directory, "first file.txt")
	second := filepath.Join(directory, "second.txt")
	if err := os.WriteFile(first, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}

	paths, ok := droppedLocalPaths(second)
	if !ok || len(paths) != 1 || paths[0] != second {
		t.Fatalf("raw Windows path = %#v, ok=%v", paths, ok)
	}
	paths, ok = droppedLocalPaths(fileDropURL(first))
	if !ok || len(paths) != 1 || paths[0] != first {
		t.Fatalf("Windows file URL = %#v, ok=%v", paths, ok)
	}
}

func fileDropURL(localPath string) string {
	escaped := strings.ReplaceAll(url.PathEscape(filepath.ToSlash(localPath)), "%2F", "/")
	if runtime.GOOS == "windows" {
		return "file:///" + escaped
	}
	return "file://" + escaped
}

func TestDroppedLocalPathsLeavesOrdinaryPasteAlone(t *testing.T) {
	for _, content := range []string{"ls -la", "echo /path/that/does/not/exist", "unterminated 'quote"} {
		if paths, ok := droppedLocalPaths(content); ok {
			t.Fatalf("ordinary paste %q recognized as %#v", content, paths)
		}
	}
}

func TestCompactSize(t *testing.T) {
	for size, want := range map[int64]string{
		0:             "0 B",
		1023:          "1023 B",
		1024:          "1.0 KB",
		12 * 1024:     "12 KB",
		10239:         "10 KB",
		850 << 20:     "850 MB",
		(1 << 20) - 1: "1.0 MB",
		1503238553:    "1.4 GB",
		5 << 40:       "5.0 TB",
	} {
		if got := compactSize(size); got != want {
			t.Errorf("compactSize(%d) = %q, want %q", size, got, want)
		}
	}
}

func TestFileRowRightAlignsSize(t *testing.T) {
	file := &fileNode{Name: "video.mp4", Size: 1503238553}
	if got, want := fileRow("· video.mp4", file, 24), "· video.mp4       1.4 GB"; got != want {
		t.Fatalf("fileRow = %q, want %q", got, want)
	}
	if got := fileRow("▸ docs", &fileNode{Name: "docs", Dir: true}, 24); strings.Contains(got, "B") {
		t.Fatalf("directory row shows a size: %q", got)
	}
	long := fileRow("· a-very-long-file-name.tar.gz", &fileNode{Size: 850 << 20}, 20)
	if lipgloss.Width(long) != 20 || !strings.HasSuffix(long, "850 MB") {
		t.Fatalf("long row = %q (width %d)", long, lipgloss.Width(long))
	}
	if got := fileRow("· file.txt", &fileNode{Size: 12 * 1024}, 12); strings.Contains(got, "KB") {
		t.Fatalf("narrow row should drop the size: %q", got)
	}
}
