# SSH Manager TUI

Терминальный менеджер SSH-подключений на Go. Хранит хосты и профили,
поддерживает вкладки, SFTP, проброс портов и встроенную SSH-консоль.

## Установка

Установщик сам выберет `amd64`/`arm64`, проверит SHA-256 и установит команду
в `/usr/local/bin`. При необходимости будет запрошен пароль `sudo`.

### Linux

```bash
curl -fsSL https://raw.githubusercontent.com/savuerka/ssh-manager-tui/main/scripts/install-linux.sh | sh
sshm
```

### macOS

```bash
curl -fsSL https://raw.githubusercontent.com/savuerka/ssh-manager-tui/main/scripts/install-macos.sh | sh
sshm
```

### Windows

В PowerShell:

```powershell
irm https://raw.githubusercontent.com/savuerka/ssh-manager-tui/main/scripts/install-windows.ps1 | iex
sshm
```

## Возможности

- SSH по паролю или ключу с проверкой host key;
- повторный запрос и сохранение нового пароля после ошибки авторизации;
- несколько независимых подключений во вкладках;
- загрузка, скачивание и управление файлами через SFTP;
- Local, Remote и Dynamic SOCKS5-пробросы портов;
- импорт хостов из Bash, Zsh и Fish history;
- группы хостов и переиспользуемые профили пользователя;
- автоматическое переподключение и встроенное обновление.

## Основные клавиши

- `Enter` — подключиться или открыть выбранный элемент;
- `n` / `e` / `d` — создать, изменить или удалить хост;
- `Ctrl+T` / `Ctrl+W` — открыть или закрыть вкладку;
- `Ctrl+B` — показать или скрыть SFTP;
- `Ctrl+Space` — переключить фокус SSH/SFTP;
- `Ctrl+D` / `Ctrl+U` — скачать или загрузить;
- `Alt+P` — открыть пробросы портов;
- `Esc`, затем `q` — отключиться; `Ctrl+Q` — выйти.

На macOS интерфейс автоматически показывает системно совместимые сочетания.

## Обновление

```bash
sshm update --check
```

Для обновления повторно запустите команду установки. В Windows сначала закройте
`sshm.exe`.

## Сборка и релиз

Нужен Go 1.25 или новее:

```bash
make test
make build
```

GitHub Release для Linux, macOS и Windows создаётся автоматически по тегу:

```bash
git tag v1.0.0
git push origin v1.0.0
```
