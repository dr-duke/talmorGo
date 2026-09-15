//go:build !unix

package downloader

import (
	"os/exec"
	"time"
)

// setupProcessGroup — заглушка для платформ без групп процессов POSIX.
// Целевая платформа проекта — Linux в контейнере, здесь важно лишь то, чтобы
// Wait не висел вечно на каналах вывода, унаследованных потомками.
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.WaitDelay = 5 * time.Second
}
