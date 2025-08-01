package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

type ytDlp struct {
	inputStrings       []string
	binaryPath         string
	outputPath         string
	outputType         string
	proxy              string
	defaultParams      []string
	customParams       []string
	processingTimeoutS int
	queue              [][]string
}

func newYtDlp() *ytDlp {
	var result = ytDlp{
		binaryPath:         "/Users/glebpyanov/Downloads/yt-dlp_macos",
		processingTimeoutS: 600,
		defaultParams: []string{
			"-t", "mp4",
			"-o", "%(title)s.%(ext)s",
			"--print", "post_process:filename",
			"--no-simulate",
		},
	}
	return &result
}

func (y *ytDlp) setOutputPath(path string) string {
	y.outputPath = path
	y.customParams = append(y.customParams, "-P", path)
	return y.outputPath

}
func (y *ytDlp) setOutputType(outputType string) string {
	y.outputType = outputType
	y.customParams = append(y.customParams, "-t", outputType)
	return y.outputPath
}

func (y *ytDlp) setBinaryPath(path string) string {
	y.binaryPath = path
	return y.binaryPath
}
func (y *ytDlp) setProxy(proxyString string) string {
	y.proxy = proxyString
	y.customParams = append(y.customParams, "--proxy", y.proxy)
	return y.proxy
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
	Output string
	Error  error
}

func (y *ytDlp) runCommand() []CommandResult {
	y.preProcessing()
	var wg sync.WaitGroup
	var outputMutex sync.Mutex
	results := make(chan CommandResult, len(y.queue))
	for _, downloadItem := range y.queue {
		wg.Add(1)
		go func(arguments []string, ch chan<- CommandResult) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(y.processingTimeoutS)*1000*time.Millisecond)
			defer cancel()
			var destinationFilepath string

			cmd := exec.CommandContext(ctx, y.binaryPath, arguments...)

			stdout, _ := cmd.StdoutPipe()
			stderr, _ := cmd.StderrPipe()

			slog.Info(fmt.Sprintf("Running %s %s", y.binaryPath, arguments))
			if err := cmd.Start(); err != nil {
				printOutput(&outputMutex, "[ОШИБКА] Не удалось запустить команду: %v\n", err)
				ch <- CommandResult{
					Output: "Executing error",
					Error:  err,
				}
			}
			scanner := func(prefix string, reader io.Reader) {
				scanner := bufio.NewScanner(reader)
				r, _ := regexp.Compile("^(" + y.outputPath + ").*\\.(\\w{3,5})$")
				for scanner.Scan() {
					line := scanner.Text()
					if r.MatchString(line) {
						destinationFilepath = line
					}
					printOutput(&outputMutex, "[Video %s] : %s %s\n", arguments[len(arguments)-1], prefix, line)
				}
			}

			// Запускаем чтение stdout и stderr
			go scanner("STDOUT", stdout)
			go scanner("STDERR", stderr)

			// Ждем завершения команды
			if err := cmd.Wait(); err != nil {
				printOutput(&outputMutex, "[Команда %d] Завершена с ошибкой: %v\n", arguments[len(arguments)-1], err)
				ch <- CommandResult{
					Output: fmt.Sprintf("Processing error %s %s", y.binaryPath, arguments),
					Error:  err,
				}
			}
			ch <- CommandResult{
				Output: destinationFilepath,
				Error:  nil,
			}
		}(downloadItem, results)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	//var rError []error
	var res []CommandResult
	for result := range results {
		res = append(res, result)
	}
	return res
}

func printOutput(mutex *sync.Mutex, format string, a ...interface{}) {
	mutex.Lock()
	defer mutex.Unlock()
	fmt.Printf(format, a...)
}
