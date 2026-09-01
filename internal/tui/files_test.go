package tui

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
