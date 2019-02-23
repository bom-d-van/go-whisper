// +build ignore

package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	whisper "github.com/go-graphite/go-whisper"
)

func main() {
	// dir, err := os.Open(os.Args[0])
	// if err != nil {
	// 	panic(err)
	// }
	var storeDir string
	var progressDB string
	var ratePerSec int
	var logFile string
	var pidFile string

	signalc := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt)

	shutdownc := make(chan struct{}, 1)
	taskc := make(chan string, ratePerSec)
	go schedule(taskc, shutdownc)
	// go initLog(shutdownc)

	for {
		log.Printf("info: start new conversion cycle")
		files, dur, err := scan(storeDir)
		if err != nil {
			return
		}
		log.Printf("info: scan %d files took %s", len(files), dur)

		cwsps, err := readProgress()
		// tasks :=

		var tasks []string
		for _, file := range files {
			if _, ok := cwsps[file]; ok {
				continue
			}
			tasks = append(tasks, file)
		}

		log.Printf("info: uncompressed %d total %d %.2f", len(tasks), len(files), float64(len(tasks))/float64(len(files)))
	}
}

var progress = struct {
}{}

func schedule(rate int, taskc chan string, shutdownc chan os.Signal) {
	ticker := time.NewTicker(time.Second / time.Duration(rate))
	for range ticker {

	}
}

func scan(dir string) (files []string, dur time.Duration, err error) {
	start := time.Now()
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".wsp") {
			files = append(files, path)
		}
		return nil
	})
	dur = time.Now().Sub(start)
	return
}

func readProgress(db string) (files map[string]struct{}, err error) {
	file, err := os.Open(db)
	if err != nil {
		return
	}
	defer file.Close()

	r := bufio.NewReader(file)
	var line string
	for {
		line, err = r.ReadLine()
		if err == io.EOF {
			err = nil
			break
		}
		if line == "" {
			continue
		}
		a := strings.Split(line, ",")
		files[a[0]] = struct{}{}
	}
	if err == io.EOF {
		err = nil
	}
	return
}

var workTokens = make(chan struct{}, runtime.NumCPU())
var convertingCount int64
var conversionLogChan = make(chan string, 64)
var conversionLogFile = "/var/lib/carbon/cwhisper.log"

func Init() {
	go logConversions()
	go func() {
		ticker := time.NewTicker(time.Second / time.Duration(cap(workTokens)))
		for range ticker.C {
			workTokens <- struct{}{}
		}
	}()
}

func SetMaxOnlineConvertionCount(count int) {
	if count > 0 {
		workTokens = make(chan struct{}, count)
	}
}
func SetLogConversionLog(path string) {
	if path != "" {
		conversionLogFile = path
	}
}
func GetCovertingFileCount() int64 { return atomic.LoadInt64(&convertingCount) }

func logConversions() {
	logf, err := os.OpenFile(conversionLogFile, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		fmt.Printf("cwhisper: failed to open %s: %s", conversionLogFile, err)
		return
	}

	for file := range conversionLogChan {
		if _, err := logf.WriteString(file); err != nil {
			fmt.Printf("cwhisper: failed to log %s: %s", file, err)
		}
		if err := logf.Sync(); err != nil {
			fmt.Printf("cwhisper: failed to sync log: %s", err)
		}
	}
}

func convert() {
	// a simple strategy of throttling auto-converstion to compressed format
	select {
	case <-workTokens:
		if atomic.LoadInt64(&convertingCount) >= int64(cap(workTokens)) {
			return nil
		}

		start := time.Now()
		var oldSize int64
		if stat, err := whisper.file.Stat(); err == nil {
			oldSize = stat.Size()
		}
		defer func() {
			var newSize int64
			if stat, err := whisper.file.Stat(); err == nil {
				newSize = stat.Size()
			}
			conversionLogChan <- fmt.Sprintf(
				"%s,%d,%d,%d,%d\n",
				whisper.file.Name(), oldSize, newSize, time.Now().Sub(start), start.Unix(),
			)
			atomic.AddInt64(&convertingCount, -1)
		}()

		atomic.AddInt64(&convertingCount, 1)
	default:
		return nil
	}

	os.Remove(whisper.file.Name() + ".cwsp")

	whisper.OpenWithOptions(whisper.file.Name()+".cwsp", whisper.Options{})

	// if err := os.Rename(whisper.file.Name(), whisper.file.Name()+".bak"); err != nil {
	// 	fmt.Printf("failed to backup %s: %s\n", whisper.file.Name(), err)
	// }
	if err := os.Rename(dst.file.Name(), whisper.file.Name()); err != nil {
		return err
	}

	whisper.file.Close()
	oldFile := whisper.file
	nwhisper, err := OpenWithOptions(whisper.file.Name(), whisper.opts)
	*whisper = *nwhisper
	for _, archive := range whisper.archives {
		archive.whisper = whisper
	}

	// whisper.log += fmt.Sprintf("%s; convert (err: %s file: %p oldFile: %p newFile: %p); ", clog, err, whisper.file, oldFile, nwhisper.file)
}
