package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"ssh-manager-tui/internal/config"
	"ssh-manager-tui/internal/tui"
	"ssh-manager-tui/internal/update"
)

var (
	version           = "dev"
	commit            = "unknown"
	buildDate         = "unknown"
	updateManifestURL = ""
)

func main() {
	if exitCode, handled := handleCommand(os.Args[1:]); handled {
		os.Exit(exitCode)
	}
	runTUI()
}

func handleCommand(arguments []string) (int, bool) {
	if len(arguments) == 0 {
		return 0, false
	}
	switch arguments[0] {
	case "version", "--version", "-v":
		fmt.Printf("sshm %s (commit %s, built %s)\n", version, commit, buildDate)
		return 0, true
	case "update":
		return runUpdate(arguments[1:]), true
	case "help", "--help", "-h":
		printHelp()
		return 0, true
	default:
		fmt.Fprintf(os.Stderr, "sshm: неизвестная команда %q\n\n", arguments[0])
		printHelp()
		return 2, true
	}
}

func runUpdate(arguments []string) int {
	checkOnly := false
	for _, argument := range arguments {
		switch argument {
		case "--check":
			checkOnly = true
		case "--help", "-h":
			fmt.Println("Использование: sshm update [--check]")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "sshm update: неизвестный параметр %q\n", argument)
			return 2
		}
	}
	manifestURL := resolvedUpdateManifestURL()
	if manifestURL == "" {
		fmt.Fprintln(os.Stderr, "sshm update: URL обновлений не настроен")
		fmt.Fprintln(os.Stderr, "задайте SSHM_UPDATE_URL или соберите приложение с make UPDATE_URL=https://…")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := update.Run(ctx, update.Options{
		CurrentVersion: version,
		ManifestURL:    manifestURL,
		Token:          strings.TrimSpace(os.Getenv("SSHM_UPDATE_TOKEN")),
		CheckOnly:      checkOnly,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "sshm update:", err)
		if os.IsPermission(err) || strings.Contains(strings.ToLower(err.Error()), "permission denied") {
			fmt.Fprintln(os.Stderr, "нет прав на замену бинарника; обновите его через make install с тем же PREFIX")
		}
		return 1
	}
	switch {
	case result.Updated:
		if result.Deferred {
			fmt.Printf("sshm %s загружен. Приложение будет перезапущено автоматически.\n", result.LatestVersion)
		} else {
			fmt.Printf("sshm обновлён: %s → %s. Перезапустите приложение.\n", result.CurrentVersion, result.LatestVersion)
		}
	case result.Available:
		fmt.Printf("Доступно обновление sshm: %s → %s\n", result.CurrentVersion, result.LatestVersion)
	default:
		fmt.Printf("Установлена актуальная версия sshm %s\n", result.CurrentVersion)
	}
	return 0
}

func resolvedUpdateManifestURL() string {
	if value := strings.TrimSpace(os.Getenv("SSHM_UPDATE_URL")); value != "" {
		return value
	}
	return updateManifestURL
}

func printHelp() {
	fmt.Println(`Использование: sshm [команда]

Без команды открывает SSH Manager TUI.

Команды:
  version          показать версию сборки
  update           проверить и установить обновление
  update --check   только проверить наличие обновления
  help             показать эту справку`)
}

func runTUI() {
	store, err := config.NewStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "configuration error:", err)
		os.Exit(1)
	}

	model, err := tui.New(store, tui.AppInfo{
		Version:           version,
		UpdateManifestURL: resolvedUpdateManifestURL(),
		UpdateToken:       strings.TrimSpace(os.Getenv("SSHM_UPDATE_TOKEN")),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup error:", err)
		os.Exit(1)
	}

	program := tea.NewProgram(model)
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "sshm:", err)
		os.Exit(1)
	}
}
