//go:build unix

package downloader

import (
	"os/exec"
	"syscall"
	"time"
)

// setupProcessGroup запускает загрузчик в собственной группе процессов и по
// отмене снимает группу целиком.
//
// yt-dlp порождает ffmpeg для склейки, и тот наследует те же каналы вывода.
// Сигнал одному прямому потомку оставлял ffmpeg сиротой: он продолжал писать
// ровно тот файл, ради отказа от которого нажали «Отменить», а чтение вывода
// не получало признака конца, пока он жив, — поэтому обработчик задания не
// возвращался и слот воркера оставался занятым до его естественного конца.
func setupProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Отрицательный pid адресует всю группу.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// Страховка на случай, если кто-то из группы всё же удержал каналы вывода:
	// Wait не должен висеть на них бесконечно.
	cmd.WaitDelay = 5 * time.Second
}
