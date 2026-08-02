package pkg

import (
	"fmt"
	"io"
	"log"
	"os"
)

type Logger struct {
	Logs chan string
}

var logger Logger

const (
	flag       = log.Ldate | log.Ltime | log.Lshortfile
	preDebug   = "[DEBUG]"
	preInfo    = "[INFO]"
	preWarning = "[WARNING]"
	preError   = "[ERROR]"
)

var (
	logFile       io.Writer
	debugLogger   *log.Logger
	infoLogger    *log.Logger
	warningLogger *log.Logger
	errorLogger   *log.Logger
)

func init() {
	logger = Logger{
		Logs: make(chan string),
	}

	// 默认写入 io.Discard；若配置开启 SaveLog 则通过 InitLogFile 替换为真实文件
	debugLogger = log.New(io.Discard, preDebug, flag)
	infoLogger = log.New(io.Discard, preInfo, flag)
	warningLogger = log.New(io.Discard, preWarning, flag)
	errorLogger = log.New(io.Discard, preError, flag)
}

// InitLogFile 根据配置开启文件日志。在 config.Start() 之后调用。
func InitLogFile() error {
	var err error
	logFile, err = os.OpenFile("./logs/game.log", os.O_CREATE|os.O_APPEND|os.O_RDWR, 0666)
	if err != nil {
		logFile, err = os.OpenFile("./game.log", os.O_CREATE|os.O_APPEND|os.O_RDWR, 0666)
		if err != nil {
			return fmt.Errorf("open log file: %w", err)
		}
	}

	debugLogger.SetOutput(logFile)
	infoLogger.SetOutput(logFile)
	warningLogger.SetOutput(logFile)
	errorLogger.SetOutput(logFile)
	INFO("log file opened:", logFile.(*os.File).Name())
	return nil
}

func DEBUG(v ...interface{}) {
	debugLogger.Print(v...)
	sprintf := fmt.Sprintf("\033[33m[DEBUG] %s\033[0m", fmt.Sprintln(v...))
	defer func() {
		logger.Logs <- sprintf
	}()
}

func INFO(v ...interface{}) {
	infoLogger.Print(v...)
	sprintf := fmt.Sprintf("\033[32m[INFO] %s\033[0m", fmt.Sprintln(v...))
	defer func() {
		logger.Logs <- sprintf
	}()
}

func WARNING(v ...interface{}) {
	warningLogger.Print(v...)
	sprintf := fmt.Sprintf("\033[34m[WARNING] %s\033[0m", fmt.Sprintln(v...))
	defer func() {
		logger.Logs <- sprintf
	}()
}

func ERROR(v ...interface{}) {
	errorLogger.Print(v...)
	sprintf := fmt.Sprintf("\033[31m[ERROR] %s\033[0m", fmt.Sprintln(v...))
	defer func() {
		logger.Logs <- sprintf
	}()
}

func (l *Logger) appendLog(log string) {
	l.Logs <- log
}

func StartLog() {
	go func() {
		for {
			select {
			case s := <-logger.Logs:
				log.Print(s)
			}
		}
	}()

}

func SetOutputPath(path string) {
	var err error
	logFile, err = os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0666)
	if err != nil {
		log.Fatalf("create log file err %+v", err)
	}
	debugLogger.SetOutput(logFile)
	infoLogger.SetOutput(logFile)
	warningLogger.SetOutput(logFile)
	errorLogger.SetOutput(logFile)
}
