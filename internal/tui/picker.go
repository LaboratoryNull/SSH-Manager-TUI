package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type pickerMode int

const (
	pickerClosed pickerMode = iota
	pickerDownloadDestination
	pickerUploadSource
)

type localEntry struct {
	name string
	path string
	dir  bool
	mode os.FileMode
	size int64
}

type localPicker struct {
	mode         pickerMode
	path         string
	entries      []localEntry
	cursor       int
	offset       int
	loading      bool
	err          error
	remoteTarget *fileNode
	marked       map[string]bool
}

type localDirLoadedMsg struct {
	path    string
	entries []localEntry
	err     error
	picker  *localPicker
}

func newLocalPicker(mode pickerMode, remoteTarget *fileNode) (*localPicker, tea.Cmd) {
	home, err := os.UserHomeDir()
	if err != nil {
		picker := &localPicker{mode: mode, path: "/", remoteTarget: remoteTarget, err: err}
		return picker, nil
	}
	start := home
	if mode == pickerDownloadDestination {
		downloads := filepath.Join(home, "Downloads")
		if info, statErr := os.Stat(downloads); statErr == nil && info.IsDir() {
			start = downloads
		}
	}
	picker := &localPicker{mode: mode, path: start, loading: true, remoteTarget: remoteTarget, marked: make(map[string]bool)}
	return picker, loadLocalDirectory(start, picker)
}

func loadLocalDirectory(directory string, picker *localPicker) tea.Cmd {
	return func() tea.Msg {
		entries, err := os.ReadDir(directory)
		if err != nil {
			return localDirLoadedMsg{path: directory, err: err, picker: picker}
		}
		result := make([]localEntry, 0, len(entries))
		for _, entry := range entries {
			info, infoErr := entry.Info()
			if infoErr != nil {
				continue
			}
			result = append(result, localEntry{
				name: entry.Name(), path: filepath.Join(directory, entry.Name()),
				dir: entry.IsDir(), mode: info.Mode(), size: info.Size(),
			})
		}
		sort.SliceStable(result, func(i, j int) bool {
			if result[i].dir != result[j].dir {
				return result[i].dir
			}
			return strings.ToLower(result[i].name) < strings.ToLower(result[j].name)
		})
		return localDirLoadedMsg{path: directory, entries: result, picker: picker}
	}
}

func (p *localPicker) selected() *localEntry {
	if p == nil || p.cursor < 0 || p.cursor >= len(p.entries) {
		return nil
	}
	return &p.entries[p.cursor]
}

func (p *localPicker) enter(directory string) tea.Cmd {
	cleaned, err := filepath.Abs(directory)
	if err != nil {
		p.err = err
		return nil
	}
	p.path, p.cursor, p.offset, p.loading, p.err = cleaned, 0, 0, true, nil
	p.entries = nil
	return loadLocalDirectory(cleaned, p)
}

func (p *localPicker) parent() tea.Cmd {
	parent := filepath.Dir(p.path)
	if parent == p.path {
		return nil
	}
	return p.enter(parent)
}

func (p *localPicker) ensureVisible(height int) {
	height = max(1, height)
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+height {
		p.offset = p.cursor - height + 1
	}
	p.offset = min(max(0, p.offset), max(0, len(p.entries)-height))
}

func formatBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}
