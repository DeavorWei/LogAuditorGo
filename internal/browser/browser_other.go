//go:build !windows

package browser

import (
	"fmt"
	"os/exec"
	"runtime"
)

func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to open browser on %s: %w", runtime.GOOS, err)
	}
	return nil
}
