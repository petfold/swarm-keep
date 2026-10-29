// Package notify shows a desktop notification.
package notify

import (
	"errors"
	"os/exec"
	"runtime"
	"strconv"
)

// ErrUnsupported means that this system has no notification command
// swarm-keep knows.
var ErrUnsupported = errors.New("desktop notifications are not supported on this system")

// Send shows title and body as a desktop notification: notify-send on
// Linux and the BSDs, osascript on macOS.
func Send(title, body string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		script := "display notification " + strconv.Quote(body) + " with title " + strconv.Quote(title)
		cmd = exec.Command("osascript", "-e", script)
	case "linux", "freebsd", "openbsd", "netbsd":
		cmd = exec.Command("notify-send", "--app-name=swarm-keep", title, body)
	default:
		return ErrUnsupported
	}
	return cmd.Run()
}
