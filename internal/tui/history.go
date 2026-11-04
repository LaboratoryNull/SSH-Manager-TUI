package tui

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"

	"ssh-manager-tui/internal/config"
)

const maxHistoryBytes = 4 << 20

type historyLoadedMsg struct {
	tabID      uint64
	candidates []importCandidate
	err        error
}

type importCandidate struct {
	session  config.Session
	selected bool
	profile  string
	group    string
}

type importWizard struct {
	loading       bool
	entries       []importCandidate
	cursor        int
	finishFocused bool
	groupInput    *textinput.Model
	groupCursor   int
	status        string
}

// readShellHistory returns the commands of Bash, Zsh and Fish history in
// chronological order, plus per-file read errors that are not fatal.
func readShellHistory() ([]string, []string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	paths := []string{
		os.Getenv("HISTFILE"), filepath.Join(home, ".bash_history"), filepath.Join(home, ".zsh_history"),
		filepath.Join(home, ".local", "share", "fish", "fish_history"),
	}
	seenPaths := make(map[string]struct{})
	var commands []string
	var readErrors []string
	for _, filename := range paths {
		if filename == "" {
			continue
		}
		filename = expandLocalHome(filename, home)
		if _, exists := seenPaths[filename]; exists {
			continue
		}
		seenPaths[filename] = struct{}{}
		data, readErr := readHistoryTail(filename, maxHistoryBytes)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			readErrors = append(readErrors, fmt.Sprintf("%s: %v", filename, readErr))
			continue
		}
		commands = append(commands, historyCommands(string(data))...)
	}
	return commands, readErrors, nil
}

func scanSSHHistory() ([]config.Session, error) {
	commands, readErrors, err := readShellHistory()
	if err != nil {
		return nil, err
	}
	hosts := historyHostsFromCommands(commands)
	if len(hosts) == 0 && len(readErrors) > 0 {
		return nil, errors.New(strings.Join(readErrors, "; "))
	}
	sessions := make([]config.Session, len(hosts))
	for index, host := range hosts {
		sessions[index] = host.session
	}
	return sessions, nil
}

// historyHost is a distinct SSH destination found in shell history together
// with how many times it was used; the newest occurrence wins for options.
type historyHost struct {
	session config.Session
	uses    int
}

// historyHostsFromCommands returns destinations newest first.
func historyHostsFromCommands(commands []string) []historyHost {
	var hosts []historyHost
	seen := make(map[string]int)
	for commandIndex := len(commands) - 1; commandIndex >= 0; commandIndex-- {
		for _, session := range sshSessionsFromCommand(commands[commandIndex]) {
			key := sessionIdentity(session)
			if index, exists := seen[key]; exists {
				hosts[index].uses++
				continue
			}
			seen[key] = len(hosts)
			hosts = append(hosts, historyHost{session: session, uses: 1})
		}
	}
	return hosts
}

func sessionIdentity(session config.Session) string {
	return strings.ToLower(session.User) + "\x00" + strings.ToLower(session.Host) + "\x00" + strconv.Itoa(session.Port)
}

func readHistoryTail(filename string, limit int64) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	start := max(int64(0), info.Size()-limit)
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return nil, err
	}
	if start > 0 {
		if newline := strings.IndexByte(string(data), '\n'); newline >= 0 {
			data = data[newline+1:]
		}
	}
	return data, nil
}

func historyCommands(data string) []string {
	var commands []string
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, ": ") {
			if separator := strings.IndexByte(line, ';'); separator >= 0 {
				line = line[separator+1:]
			}
		}
		if strings.HasPrefix(line, "- cmd:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "- cmd:"))
			line = strings.ReplaceAll(line, "\\n", "\n")
		}
		commands = append(commands, line)
	}
	return commands
}

func sshSessionsFromCommand(command string) []config.Session {
	words, ok := shellHistoryWords(command)
	if !ok {
		return nil
	}
	var sessions []config.Session
	for index := 0; index < len(words); index++ {
		if filepath.Base(words[index]) != "ssh" {
			continue
		}
		if session, consumed, ok := parseSSHWords(words[index+1:]); ok {
			sessions = append(sessions, session)
			index += consumed
		}
	}
	return sessions
}

// parseSSHWords reads the arguments of an ssh command line: options, the
// destination and, like OpenSSH, options that follow the destination
// ("ssh host -p 2222"). The first plain word after the destination starts
// the remote command and ends parsing. It returns how many words were read.
func parseSSHWords(words []string) (config.Session, int, bool) {
	session := config.Session{User: currentUser()}
	explicitPort, targetPort := 0, 0
	explicitUser, targetUser := "", ""
	found := false
	pending := ""
	apply := func(option, value string) {
		switch option {
		case "port":
			explicitPort = parsePortOption(value)
		case "user":
			explicitUser = value
		case "key":
			session.KeyPath = value
		case "config":
			applySSHConfigOption(value, &explicitPort, &explicitUser, &session.KeyPath)
		}
	}
	index := 0
	for ; index < len(words); index++ {
		word := words[index]
		if word == ";" || word == "|" || word == "&&" || word == "||" {
			break
		}
		if pending != "" {
			apply(pending, word)
			pending = ""
			continue
		}
		if strings.HasPrefix(word, "-") && word != "-" {
			option, inline := sshOptionName(word)
			switch {
			case option == "" && sshOptionTakesValue(word):
				pending = "ignore"
			case option != "" && inline != "":
				apply(option, inline)
			case option != "":
				pending = option
			}
			continue
		}
		if found {
			break // the remote command
		}
		host, userName, port, ok := parseSSHTarget(word)
		if !ok {
			return config.Session{}, index, false
		}
		session.Host, targetUser, targetPort, found = host, userName, port, true
	}
	if !found || pending != "" {
		return config.Session{}, index, false
	}
	// Explicit options win over the user@host:port shorthand.
	session.Port = 22
	if targetPort > 0 {
		session.Port = targetPort
	}
	if explicitPort != 0 {
		session.Port = explicitPort
	}
	if targetUser != "" {
		session.User = targetUser
	}
	if explicitUser != "" {
		session.User = explicitUser
	}
	if session.Port < 1 || session.Port > 65535 || session.User == "" {
		return config.Session{}, index, false
	}
	session.Name = session.Host
	return session, index, true
}

// sshOptionName maps -p/-l/-i/-o (with an optional inline value such as
// "-p2222" or "-oPort=2222") to what they set.
func sshOptionName(word string) (option, inline string) {
	if len(word) < 2 {
		return "", ""
	}
	inline = word[2:]
	switch word[:2] {
	case "-p":
		return "port", inline
	case "-l":
		return "user", inline
	case "-i":
		return "key", inline
	case "-o":
		return "config", inline
	}
	return "", ""
}

// applySSHConfigOption handles "-o Port=2222", "-o 'Port 2222'", User and
// IdentityFile; other options are ignored.
func applySSHConfigOption(value string, port *int, userName, keyPath *string) {
	name, setting, ok := strings.Cut(strings.TrimSpace(value), "=")
	if !ok {
		name, setting, _ = strings.Cut(strings.TrimSpace(value), " ")
	}
	setting = strings.TrimSpace(setting)
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "port":
		*port = parsePortOption(setting)
	case "user":
		*userName = setting
	case "identityfile":
		*keyPath = setting
	}
}

func sshOptionTakesValue(option string) bool {
	switch option {
	case "-b", "-B", "-c", "-D", "-E", "-e", "-F", "-I", "-J", "-L", "-m", "-O", "-o", "-Q", "-R", "-S", "-W", "-w":
		return true
	}
	return false
}

func parseSSHTarget(target string) (host, userName string, port int, ok bool) {
	if strings.HasPrefix(target, "ssh://") {
		u, err := url.Parse(target)
		if err != nil || u.Hostname() == "" {
			return "", "", 0, false
		}
		host = u.Hostname()
		if u.User != nil {
			userName = u.User.Username()
		}
		if u.Port() != "" {
			port, _ = strconv.Atoi(u.Port())
		}
		return host, userName, port, true
	}
	if at := strings.LastIndex(target, "@"); at >= 0 {
		userName, target = target[:at], target[at+1:]
	}
	if target == "" || strings.ContainsAny(target, " \t\r\n") {
		return "", "", 0, false
	}
	if parsedHost, parsedPort, err := net.SplitHostPort(target); err == nil {
		host, port = parsedHost, mustAtoi(parsedPort)
	} else {
		host = strings.Trim(target, "[]")
	}
	return host, userName, port, host != ""
}

func shellHistoryWords(command string) ([]string, bool) {
	var words []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
	}
	for _, r := range command {
		if escaped {
			current.WriteRune(r)
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
				current.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		if strings.ContainsRune(";|&", r) {
			flush()
			separator := string(r)
			if len(words) > 0 && (r == '|' || r == '&') && words[len(words)-1] == separator {
				words[len(words)-1] += separator
			} else {
				words = append(words, separator)
			}
			continue
		}
		current.WriteRune(r)
	}
	if escaped || quote != 0 {
		return nil, false
	}
	flush()
	return words, true
}

func expandLocalHome(filename, home string) string {
	if filename == "~" || strings.HasPrefix(filename, "~/") {
		return filepath.Join(home, strings.TrimPrefix(filename, "~/"))
	}
	return filename
}

// parsePortOption returns -1 for a value that is not a number, so an
// explicit but broken port rejects the command instead of falling back to 22.
func parsePortOption(value string) int {
	number, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return -1
	}
	return number
}

func mustAtoi(value string) int {
	number, _ := strconv.Atoi(value)
	return number
}
