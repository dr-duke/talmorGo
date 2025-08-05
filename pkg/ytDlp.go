package main

import (
	"bufio"
	"context"
	"fmt"
	"github.com/jessevdk/go-flags"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type ytDlp struct {
	inputStrings       []string
	BinaryPath         string `long:"yt-dlp-binary-path" env:"YT_DLP_BINARY" default:"./yt-dlp"`
	OutputPath         string `long:"yt-dlp-output-dir" env:"YT_DLP_OUTPUT_DIR" default:"./"`
	OutputType         string `long:"yt-dlp-output-format" env:"YT_DLP_OUTPUT_FORMAT" default:"mp4"`
	Proxy              string `long:"yt-dlp-proxy" env:"YT_DLP_PROXY" default:""`
	defaultParams      []string
	customParams       []string
	ProcessingTimeoutS int `long:"yt-dlp-processing-timeout" env:"YT_DLP_PROCESSING_TIMEOUT" default:"300"`
	queue              [][]string
}

func newYtDlp() *ytDlp {
	var result ytDlp
	parser := flags.NewParser(&result, flags.IgnoreUnknown)
	_, err := parser.ParseArgs(os.Args)
	if err != nil {
		panic(fmt.Sprintf("Error while yt-dlp worker creation: %s", err))
	}
	result.defaultParams = []string{
		"-o", "%(title)s.%(ext)s",
		"--print", "post_process:filename",
		"--no-simulate",
	}
	if result.Proxy != "" {
		result.setProxy(result.Proxy)
	}
	if result.OutputType != "" {
		result.setOutputType(result.OutputType)
	}
	if result.OutputPath != "" {
		result.setOutputPath(result.OutputPath)
	}
	return &result
}

func (y *ytDlp) setOutputPath(path string) string {
	y.customParams = append(y.customParams, "-P", path)
	return y.OutputPath

}
func (y *ytDlp) setOutputType(outputType string) string {
	y.customParams = append(y.customParams, "-t", outputType)
	return y.OutputPath
}

func (y *ytDlp) setProxy(proxyString string) []string {
	y.customParams = append(y.customParams, "--proxy", proxyString)
	return y.customParams
}

func (y *ytDlp) preProcessing() {
	for _, inputString := range y.inputStrings {
		for _, subStr := range strings.Split(inputString, " ") {
			var argumetsArray []string

			if _, err := url.ParseRequestURI(subStr); err != nil {
				slog.Error(fmt.Sprintf("%s is not valid url. Skipping.", subStr))
			} else {
				argumetsArray = append(argumetsArray, y.defaultParams...)
				argumetsArray = append(argumetsArray, y.customParams...)
				argumetsArray = append(argumetsArray, subStr)

				y.queue = append(y.queue, argumetsArray)
			}
		}
	}
}

type CommandResult struct {
	FileName string
	Output   string
	Error    error
}

func (y *ytDlp) flushQueue() [][]string {
	y.queue = [][]string{}
	slog.Info("ytDlp queue flushed.")
	return y.queue
}

func (y *ytDlp) runCommand() <-chan CommandResult {
	y.preProcessing()
	defer y.flushQueue()
	var wg sync.WaitGroup
	var outputMutex sync.Mutex
	results := make(chan CommandResult)
	for _, downloadItem := range y.queue {
		wg.Add(1)
		go func(arguments []string, ch chan<- CommandResult) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(y.ProcessingTimeoutS)*1000*time.Millisecond)
			defer cancel()

			cmd := exec.CommandContext(ctx, y.BinaryPath, arguments...)

			stdout, _ := cmd.StdoutPipe()
			stderr, _ := cmd.StderrPipe()

			slog.Info(fmt.Sprintf("Running %s %s", y.BinaryPath, arguments))
			if err := cmd.Start(); err != nil {
				printOutput(&outputMutex, "[ОШИБКА] Не удалось запустить команду: %v\n", err)
				ch <- CommandResult{
					Output: "Executing error",
					Error:  err,
				}
			}
			scanner := func(prefix string, reader io.Reader) {
				scanner := bufio.NewScanner(reader)
				r, _ := regexp.Compile("^(" + y.OutputPath + ").*\\.(\\w{3,5})$")
				for scanner.Scan() {
					line := scanner.Text()
					if r.MatchString(line) {
						printOutput(&outputMutex, "[Video %s] : %s %s\n", arguments[len(arguments)-1], prefix, line)
						ch <- CommandResult{
							FileName: filepath.Base(line),
							Output:   line,
							Error:    nil,
						}
					}
				}
			}

			// Запускаем чтение stdout и stderr
			go scanner("STDOUT", stdout)
			go scanner("STDERR", stderr)

			// Ждем завершения команды
			if err := cmd.Wait(); err != nil {
				printOutput(&outputMutex, "[Команда %d] Завершена с ошибкой: %v\n", arguments[len(arguments)-1], err)
				ch <- CommandResult{
					Output: fmt.Sprintf("Processing error %s %s", y.BinaryPath, arguments),
					Error:  err,
				}
			}
		}(downloadItem, results)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	return results
}

func printOutput(mutex *sync.Mutex, format string, a ...interface{}) {
	mutex.Lock()
	defer mutex.Unlock()
	fmt.Printf(format, a...)
}
