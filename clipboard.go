package main

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// copyToClipboard puts text on the system clipboard using the tool each
// platform provides for it.
func copyToClipboard(text string) error {
	var tools [][]string
	switch runtime.GOOS {
	case "windows":
		tools = [][]string{{"clip"}}
	case "darwin":
		tools = [][]string{{"pbcopy"}}
	default:
		tools = [][]string{
			{"wl-copy"},
			{"xclip", "-selection", "clipboard"},
			{"xsel", "--clipboard", "--input"},
			{"clip.exe"}, // WSL
		}
	}
	for _, tool := range tools {
		path, err := exec.LookPath(tool[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(path, tool[1:]...)
		cmd.Stdin = strings.NewReader(text)
		return cmd.Run()
	}
	return errors.New("no clipboard tool found")
}
