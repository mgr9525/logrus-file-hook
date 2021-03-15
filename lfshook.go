// Package lfshook is hook for sirupsen/logrus that used for writing the logs to local files.
package loglfshook

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"io"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sync"
)

// We are logging to file, strip colors to make the output more readable.
var defaultFormatter = &logrus.TextFormatter{DisableColors: true}

// PathMap is map for mapping a log level to a file's path.
// Multiple levels may share a file, but multiple files may not be used for one level.
type PathMap map[logrus.Level]string

// WriterMap is map for mapping a log level to an io.Writer.
// Multiple levels may share a writer, but multiple writers may not be used for one level.
type WriterMap map[logrus.Level]io.Writer

// LfsHook is a hook to handle writing to local log files.
type lfsFile struct {
	lk   sync.Mutex
	fd   *os.File
	path string
	ln   int64
}
type LfsHook struct {
	paths     PathMap
	writers   WriterMap
	levels    []logrus.Level
	lock      *sync.Mutex
	formatter logrus.Formatter

	defaultPath      string
	defaultWriter    io.Writer
	hasDefaultPath   bool
	hasDefaultWriter bool

	FdMaxLen  int
	FdMaxSize int64

	flk sync.Mutex
	fls map[logrus.Level]*lfsFile
}

// NewHook returns new LFS hook.
// Output can be a string, io.Writer, WriterMap or PathMap.
// If using io.Writer or WriterMap, user is responsible for closing the used io.Writer.
func NewLfsHook(output interface{}, formatter logrus.Formatter, maxsz ...int64) *LfsHook {
	hook := &LfsHook{
		lock:      new(sync.Mutex),
		FdMaxLen:  10,
		FdMaxSize: 1024 * 1024 * 10,
		fls:       make(map[logrus.Level]*lfsFile),
	}
	if len(maxsz) > 0 && maxsz[0] > 0 {
		hook.FdMaxSize = maxsz[0]
	}
	if len(maxsz) > 1 && maxsz[1] > 0 {
		hook.FdMaxLen = int(maxsz[1])
	}

	hook.SetFormatter(formatter)

	switch output.(type) {
	case string:
		hook.SetDefaultPath(output.(string))
		break
	case io.Writer:
		hook.SetDefaultWriter(output.(io.Writer))
		break
	case PathMap:
		hook.paths = output.(PathMap)
		for level := range output.(PathMap) {
			hook.levels = append(hook.levels, level)
		}
		break
	case WriterMap:
		hook.writers = output.(WriterMap)
		for level := range output.(WriterMap) {
			hook.levels = append(hook.levels, level)
		}
		break
	default:
		panic(fmt.Sprintf("unsupported level map type: %v", reflect.TypeOf(output)))
	}

	return hook
}

// SetFormatter sets the format that will be used by hook.
// If using text formatter, this method will disable color output to make the log file more readable.
func (hook *LfsHook) SetFormatter(formatter logrus.Formatter) {
	hook.lock.Lock()
	defer hook.lock.Unlock()
	if formatter == nil {
		formatter = defaultFormatter
	} else {
		switch formatter.(type) {
		case *logrus.TextFormatter:
			textFormatter := formatter.(*logrus.TextFormatter)
			textFormatter.DisableColors = true
		}
	}

	hook.formatter = formatter
}

// SetDefaultPath sets default path for levels that don't have any defined output path.
func (hook *LfsHook) SetDefaultPath(defaultPath string) {
	hook.lock.Lock()
	defer hook.lock.Unlock()
	hook.defaultPath = defaultPath
	hook.hasDefaultPath = true
}

// SetDefaultWriter sets default writer for levels that don't have any defined writer.
func (hook *LfsHook) SetDefaultWriter(defaultWriter io.Writer) {
	hook.lock.Lock()
	defer hook.lock.Unlock()
	hook.defaultWriter = defaultWriter
	hook.hasDefaultWriter = true
}

// Fire writes the log file to defined path or using the defined writer.
// User who run this function needs write permissions to the file or directory if the file does not yet exist.
func (hook *LfsHook) Fire(entry *logrus.Entry) error {
	hook.lock.Lock()
	defer hook.lock.Unlock()
	if hook.writers != nil || hook.hasDefaultWriter {
		return hook.ioWrite(entry)
	} else if hook.paths != nil || hook.hasDefaultPath {
		return hook.fileWrite(entry)
	}

	return nil
}

// Write a log line to an io.Writer.
func (hook *LfsHook) ioWrite(entry *logrus.Entry) error {
	var (
		writer io.Writer
		msg    []byte
		err    error
		ok     bool
	)

	if writer, ok = hook.writers[entry.Level]; !ok {
		if hook.hasDefaultWriter {
			writer = hook.defaultWriter
		} else {
			return nil
		}
	}

	// use our formatter instead of entry.String()
	msg, err = hook.formatter.Format(entry)

	if err != nil {
		log.Println("failed to generate string for entry:", err)
		return err
	}
	_, err = writer.Write(msg)
	return err
}

func (c *LfsHook) fileBakLen(path string) int {
	ln := 0
	for i := 1; i <= c.FdMaxLen; i++ {
		_, err := os.Stat(fmt.Sprintf("%s.%d", path, i))
		if !os.IsNotExist(err) {
			ln++
		} else {
			break
		}
	}
	return ln
}
func (c *LfsHook) fileBakMove(path string) {
	os.RemoveAll(fmt.Sprintf("%s.%d", path, 1))

	for i := 1; i < c.FdMaxLen; i++ {
		os.Rename(fmt.Sprintf("%s.%d", path, i+1), fmt.Sprintf("%s.%d", path, i))
	}
}
func (c *LfsHook) fileCheck(fe *lfsFile) error {
	fe.lk.Lock()
	defer fe.lk.Unlock()
	for {
		if fe.fd == nil {
			fe.ln = 0
			stat, err := os.Stat(fe.path)
			if err == nil {
				fe.ln = stat.Size()
			}
			fl, err := os.OpenFile(fe.path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0664)
			if err != nil {
				return err
			}
			fe.fd = fl
		} else if fe.ln > c.FdMaxSize {
			fe.fd.Close()
			fe.fd = nil
			ln := c.fileBakLen(fe.path)
			if ln >= c.FdMaxLen {
				c.fileBakMove(fe.path)
				os.Rename(fe.path, fmt.Sprintf("%s.%d", fe.path, ln))
			} else {
				os.Rename(fe.path, fmt.Sprintf("%s.%d", fe.path, ln+1))
			}
		} else {
			break
		}
	}

	return nil
}

// Write a log line directly to a file.
func (hook *LfsHook) fileWrite(entry *logrus.Entry) error {
	var (
		msg []byte
		err error
	)

	hook.flk.Lock()
	fe, ok := hook.fls[entry.Level]
	hook.flk.Unlock()
	if !ok {
		var path string
		if path, ok = hook.paths[entry.Level]; !ok {
			if hook.hasDefaultPath {
				path = hook.defaultPath
			} else {
				return nil
			}
		}
		os.MkdirAll(filepath.Dir(path), 0755)
		fe = &lfsFile{
			path: path,
			ln:   0,
		}
		hook.flk.Lock()
		hook.fls[entry.Level] = fe
		hook.flk.Unlock()
	}

	err = hook.fileCheck(fe)
	if err != nil {
		return err
	}

	// use our formatter instead of entry.String()
	msg, err = hook.formatter.Format(entry)

	if err != nil {
		log.Println("failed to generate string for entry:", err)
		return err
	}
	n, _ := fe.fd.Write(msg)
	fe.ln += int64(n)
	return nil
}

// Levels returns configured log levels.
func (hook *LfsHook) Levels() []logrus.Level {
	return logrus.AllLevels
}
