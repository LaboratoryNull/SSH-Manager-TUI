//go:build windows

package update

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

const windowsUpdateScript = `param(
    [Parameter(Mandatory=$true)][int]$ProcessId,
    [Parameter(Mandatory=$true)][string]$Source,
    [Parameter(Mandatory=$true)][string]$Target
)
$ErrorActionPreference = 'Stop'
try {
    Wait-Process -Id $ProcessId -ErrorAction SilentlyContinue
    $replaced = $false
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        try {
            Move-Item -LiteralPath $Source -Destination $Target -Force
            $replaced = $true
            break
        } catch {
            Start-Sleep -Milliseconds 200
        }
    }
    if (-not $replaced) { throw 'Unable to replace the application executable' }
    Start-Process -FilePath $Target
} finally {
    Remove-Item -LiteralPath $PSCommandPath -Force -ErrorAction SilentlyContinue
}
`

// Windows keeps a running executable locked. Start a detached helper which
// waits for this process to exit, replaces the verified binary and relaunches it.
func installExecutable(source, target string) (bool, error) {
	script, err := os.CreateTemp("", "sshm-update-*.ps1")
	if err != nil {
		return false, fmt.Errorf("create Windows update helper: %w", err)
	}
	scriptName := script.Name()
	keepScript := false
	defer func() {
		_ = script.Close()
		if !keepScript {
			_ = os.Remove(scriptName)
		}
	}()
	if _, err := script.WriteString(windowsUpdateScript); err != nil {
		return false, fmt.Errorf("write Windows update helper: %w", err)
	}
	if err := script.Close(); err != nil {
		return false, fmt.Errorf("close Windows update helper: %w", err)
	}
	powerShell, err := exec.LookPath("powershell.exe")
	if err != nil {
		return false, fmt.Errorf("find PowerShell: %w", err)
	}
	command := exec.Command(
		powerShell, "-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", scriptName,
		"-ProcessId", strconv.Itoa(os.Getpid()),
		"-Source", source,
		"-Target", target,
	)
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008, // DETACHED_PROCESS
	}
	if err := command.Start(); err != nil {
		return false, fmt.Errorf("start Windows update helper: %w", err)
	}
	if command.Process != nil {
		_ = command.Process.Release()
	}
	keepScript = true
	return true, nil
}
