package cli

import (
	"fmt"
	"os/exec"
	"runtime"
)

var openConsoleFn = openConsole

func openConsole(rawURL string) error {
	command, err := browserCommand(rawURL)
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

func browserCommand(rawURL string) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", rawURL), nil
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL), nil
	default:
		if _, err := exec.LookPath("xdg-open"); err != nil {
			return nil, fmt.Errorf("xdg-open is not available")
		}
		return exec.Command("xdg-open", rawURL), nil
	}
}
