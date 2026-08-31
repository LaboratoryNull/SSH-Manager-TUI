package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/pkg/sftp"
)

type fileNode struct {
	Name     string
	Path     string
	Dir      bool
	Size     int64
	Depth    int
	Expanded bool
	Loaded   bool
	Loading  bool
	Children []*fileNode
}

type dirLoadedMsg struct {
	node     *fileNode
	children []*fileNode
	err      error
}

type transferProgressMsg struct {
	done      int64
	total     int64
	path      string
	err       error
	final     bool
	uploading bool
	source    <-chan transferProgressMsg
}

func loadDir(client *sftp.Client, node *fileNode) tea.Cmd {
	return func() tea.Msg {
		entries, err := client.ReadDir(node.Path)
		if err != nil {
			return dirLoadedMsg{node: node, err: err}
		}
		children := make([]*fileNode, 0, len(entries))
		for _, entry := range entries {
			if _, err := safeChildName(entry.Name()); err != nil {
				continue
			}
			children = append(children, &fileNode{
				Name: entry.Name(), Path: path.Join(node.Path, entry.Name()),
				Dir: entry.IsDir(), Size: entry.Size(), Depth: node.Depth + 1,
			})
		}
		sort.SliceStable(children, func(i, j int) bool {
			if children[i].Dir != children[j].Dir {
				return children[i].Dir
			}
			return strings.ToLower(children[i].Name) < strings.ToLower(children[j].Name)
		})
		return dirLoadedMsg{node: node, children: children}
	}
}

func flattenTree(root *fileNode) []*fileNode {
	if root == nil {
		return nil
	}
	result := []*fileNode{root}
	var visit func(*fileNode)
	visit = func(node *fileNode) {
		if !node.Dir || !node.Expanded {
			return
		}
		for _, child := range node.Children {
			result = append(result, child)
			visit(child)
		}
	}
	visit(root)
	return result
}

func startDownload(ctx context.Context, client *sftp.Client, node *fileNode, destination string, updates chan<- transferProgressMsg) {
	defer close(updates)
	total, err := remoteSize(ctx, client, node.Path)
	if err != nil {
		updates <- transferProgressMsg{path: node.Path, err: err, final: true}
		return
	}
	base := safeBase(node.Path)
	target := availablePath(filepath.Join(destination, base))
	if err := os.MkdirAll(destination, 0o755); err != nil {
		updates <- transferProgressMsg{path: target, total: total, err: err, final: true}
		return
	}
	var done int64
	err = copyRemote(ctx, client, node.Path, target, &done, total, updates)
	updates <- transferProgressMsg{done: done, total: total, path: target, err: err, final: true}
}

func startUploads(ctx context.Context, client *sftp.Client, localSources []string, remoteDirectory string, updates chan<- transferProgressMsg) {
	defer close(updates)
	if len(localSources) == 0 {
		updates <- transferProgressMsg{path: remoteDirectory, err: errors.New("no local files selected"), final: true, uploading: true}
		return
	}
	var total int64
	for _, localSource := range localSources {
		size, err := localSize(ctx, localSource)
		if err != nil {
			updates <- transferProgressMsg{path: localSource, err: err, final: true, uploading: true}
			return
		}
		total += size
	}
	var done int64
	finalPath := remoteDirectory
	for _, localSource := range localSources {
		target, err := availableRemotePath(client, path.Join(remoteDirectory, filepath.Base(localSource)))
		if err != nil {
			updates <- transferProgressMsg{done: done, total: total, path: remoteDirectory, err: err, final: true, uploading: true}
			return
		}
		if len(localSources) == 1 {
			finalPath = target
		}
		if err := copyLocal(ctx, client, localSource, target, &done, total, updates); err != nil {
			updates <- transferProgressMsg{done: done, total: total, path: target, err: err, final: true, uploading: true}
			return
		}
	}
	updates <- transferProgressMsg{done: done, total: total, path: finalPath, final: true, uploading: true}
}

// droppedLocalPaths recognizes the text terminal emulators insert for files
// dropped from a graphical file manager. Depending on the emulator this is a
// shell-quoted path, an escaped path, or a text/uri-list containing file:// URLs.
// Returning ok=false is important: ordinary pasted commands must still reach
// the remote shell unchanged.
func droppedLocalPaths(content string) (paths []string, ok bool) {
	tokens, valid := splitDroppedText(content)
	if !valid || len(tokens) == 0 {
		return nil, false
	}
	seen := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		localPath := token
		if strings.HasPrefix(token, "file://") {
			u, err := url.Parse(token)
			if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") {
				return nil, false
			}
			localPath, err = url.PathUnescape(u.EscapedPath())
			if err != nil {
				return nil, false
			}
		}
		if !filepath.IsAbs(localPath) {
			return nil, false
		}
		localPath = filepath.Clean(localPath)
		if _, err := os.Lstat(localPath); err != nil {
			return nil, false
		}
		if _, exists := seen[localPath]; !exists {
			seen[localPath] = struct{}{}
			paths = append(paths, localPath)
		}
	}
	return paths, len(paths) > 0
}

func splitDroppedText(content string) ([]string, bool) {
	content = strings.TrimSpace(content)
	if content == "" || strings.ContainsRune(content, '\x00') {
		return nil, false
	}
	// freedesktop drag-and-drop uses text/uri-list with one URI per line.
	lines := strings.FieldsFunc(content, func(r rune) bool { return r == '\n' || r == '\r' })
	allURIs := len(lines) > 0
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "file://") {
			allURIs = false
			break
		}
	}
	if allURIs {
		result := make([]string, 0, len(lines))
		for _, line := range lines {
			result = append(result, strings.TrimSpace(line))
		}
		return result, true
	}

	var result []string
	var token strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if token.Len() > 0 {
			result = append(result, token.String())
			token.Reset()
		}
	}
	for _, r := range content {
		if escaped {
			token.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				token.WriteRune(r)
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t', '\r', '\n':
			flush()
		default:
			token.WriteRune(r)
		}
	}
	if escaped || quote != 0 {
		return nil, false
	}
	flush()
	return result, true
}

func localSize(ctx context.Context, localPath string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	info, err := os.Lstat(localPath)
	if err != nil {
		return 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("symbolic links are not uploaded: %s", localPath)
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return 0, fmt.Errorf("unsupported local file type: %s", localPath)
		}
		return info.Size(), nil
	}
	entries, err := os.ReadDir(localPath)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		size, err := localSize(ctx, filepath.Join(localPath, entry.Name()))
		if err != nil {
			return 0, err
		}
		total += size
	}
	return total, nil
}

func copyLocal(ctx context.Context, client *sftp.Client, localPath, remotePath string, done *int64, total int64, updates chan<- transferProgressMsg) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Lstat(localPath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symbolic links are not uploaded: %s", localPath)
	}
	if info.IsDir() {
		if err := client.MkdirAll(remotePath); err != nil {
			return err
		}
		entries, err := os.ReadDir(localPath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name, err := safeChildName(entry.Name())
			if err != nil {
				return err
			}
			if err := copyLocal(ctx, client, filepath.Join(localPath, name), path.Join(remotePath, name), done, total, updates); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported local file type: %s", localPath)
	}
	localFile, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer localFile.Close()
	tmp := fmt.Sprintf("%s.ssh-manager.part-%d", remotePath, time.Now().UnixNano())
	remoteFile, err := client.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY)
	if err != nil {
		return err
	}
	copyErr := copyWithProgress(ctx, remoteFile, localFile, done, total, remotePath, true, updates)
	closeErr := remoteFile.Close()
	if copyErr != nil {
		_ = client.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = client.Remove(tmp)
		return closeErr
	}
	if err := client.Chmod(tmp, info.Mode().Perm()); err != nil {
		_ = client.Remove(tmp)
		return err
	}
	if err := client.Rename(tmp, remotePath); err != nil {
		_ = client.Remove(tmp)
		return err
	}
	return nil
}

func availableRemotePath(client *sftp.Client, target string) (string, error) {
	if _, err := client.Lstat(target); os.IsNotExist(err) {
		return target, nil
	} else if err != nil {
		return "", err
	}
	ext := time.Now().Format("20060102-150405")
	for i := 0; ; i++ {
		candidate := fmt.Sprintf("%s-%s", target, ext)
		if i > 0 {
			candidate = fmt.Sprintf("%s-%s-%d", target, ext, i)
		}
		if _, err := client.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
}

func remoteSize(ctx context.Context, client *sftp.Client, remotePath string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	info, err := client.Lstat(remotePath)
	if err != nil {
		return 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("symbolic links are not downloaded: %s", remotePath)
	}
	if !info.IsDir() {
		if !info.Mode().IsRegular() {
			return 0, fmt.Errorf("unsupported remote file type: %s", remotePath)
		}
		return info.Size(), nil
	}
	entries, err := client.ReadDir(remotePath)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, entry := range entries {
		name, err := safeChildName(entry.Name())
		if err != nil {
			return 0, err
		}
		size, err := remoteSize(ctx, client, path.Join(remotePath, name))
		if err != nil {
			return 0, err
		}
		total += size
	}
	return total, nil
}

func copyRemote(ctx context.Context, client *sftp.Client, remotePath, localPath string, done *int64, total int64, updates chan<- transferProgressMsg) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := client.Lstat(remotePath)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symbolic links are not downloaded: %s", remotePath)
	}
	if info.IsDir() {
		if err := os.MkdirAll(localPath, 0o755); err != nil {
			return err
		}
		entries, err := client.ReadDir(remotePath)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			name, err := safeChildName(entry.Name())
			if err != nil {
				return err
			}
			if err := copyRemote(ctx, client, path.Join(remotePath, name), filepath.Join(localPath, name), done, total, updates); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported remote file type: %s", remotePath)
	}
	remoteFile, err := client.Open(remotePath)
	if err != nil {
		return err
	}
	defer remoteFile.Close()
	tmp := localPath + ".part"
	localFile, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	copyErr := copyWithProgress(ctx, localFile, remoteFile, done, total, localPath, false, updates)
	closeErr := localFile.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, localPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func copyWithProgress(ctx context.Context, dst io.Writer, src io.Reader, done *int64, total int64, name string, uploading bool, updates chan<- transferProgressMsg) error {
	buf := make([]byte, 128*1024)
	last := time.Now().Add(-time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			written, writeErr := dst.Write(buf[:n])
			*done += int64(written)
			if time.Since(last) >= 80*time.Millisecond || *done == total {
				select {
				case updates <- transferProgressMsg{done: *done, total: total, path: name, uploading: uploading}:
				default:
				}
				last = time.Now()
			}
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func safeBase(remotePath string) string {
	base := path.Base(path.Clean(remotePath))
	if base == "." || base == "/" || base == "" {
		return "remote-root"
	}
	return base
}

func availablePath(target string) string {
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return target
	}
	ext := time.Now().Format("20060102-150405")
	for i := 0; ; i++ {
		candidate := fmt.Sprintf("%s-%s", target, ext)
		if i > 0 {
			candidate = fmt.Sprintf("%s-%s-%d", target, ext, i)
		}
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
}

func safeChildName(name string) (string, error) {
	if name == "" || name == "." || name == ".." || path.Base(name) != name || filepath.Base(name) != name {
		return "", fmt.Errorf("unsafe remote file name: %q", name)
	}
	return name, nil
}
