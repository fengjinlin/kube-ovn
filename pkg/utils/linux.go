package utils

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

func EvalCommandSymlinks(cmd string) (string, error) {
	path, err := exec.LookPath(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to search for command %q: %v", cmd, err)
	}
	file, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("failed to read evaluate symbolic links for file %q: %v", path, err)
	}

	return file, nil
}
