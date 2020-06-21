// Package console contains Sauron's CLI commands.
package console

import (
	"encoding/json"
	"fmt"
	"github.com/BurntSushi/toml"
	"github.com/Sirupsen/logrus"
	"github.com/etcinit/sauron/eye"
	"github.com/jasonlvhit/gocron"
	"gopkg.in/urfave/cli.v1"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

type duration struct {
	time.Duration
}

func (d *duration) UnmarshalText(text []byte) error {
	var err error
	d.Duration, err = time.ParseDuration(string(text))
	return err
}

type Config struct {
	Watch      []watch
	Log        string // sauron log
	Pool       bool
	LogLevel   string
	PrefixTime bool // prefix time to every output line
	PrefixPath bool // prefix file path to every output line (default)
}

type watch struct {
	Paths              []string
	FilePattern        string // file extension pattern
	FileIgnorePattern  string
	FileIgnoreDuration duration
	FileFollowDuration duration
	PathPattern        string // path pattern
	LinePattern        string // pattern to match
	LineIgnorePattern  string // pattern to ignore
	Out                string // file to write
	Desc               string
}

var logger *logrus.Logger

// MainAction is the main action executed when using Sauron.
func MainAction(c *cli.Context) {
	done := make(chan bool)
	writePidFile(c)

	conf, result := setConfig(c)
	if !result {
		return
	}

	setLogger(conf)

	options := &eye.TrailOptions{
		PollChanges: conf.Pool,
		Logger:      logger,
	}

	if logrus.GetLevel() == logrus.DebugLevel {
		s := gocron.NewScheduler()
		s.Every(10).Seconds().Do(printMemUsage)
		<-s.Start()
	}

	for _, w := range conf.Watch {
		if outLog, err := os.OpenFile(w.Out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {

			if len(w.FilePattern) > 0 {
				if r, err := regexp.Compile(w.FilePattern); err == nil {
					logger.Debugln("FilePatternRegex created")
					options.FileReg = r
				} else {
					logger.Errorln(err)
				}
			}

			if len(w.FileIgnorePattern) > 0 {
				if r, err := regexp.Compile(w.FileIgnorePattern); err == nil {
					options.FileIgnoreReg = r
				} else {
					logger.Errorln(err)
				}
			}

			if w.FileIgnoreDuration.Duration > 0 {
				options.FileIgnoreDuration = w.FileIgnoreDuration.Duration
			} else {
				d, _ := time.ParseDuration("24h")
				options.FileIgnoreDuration = d * 7
			}

			if w.FileFollowDuration.Duration > 0 {
				options.FileFollowDuration = w.FileFollowDuration.Duration
			} else {
				d, _ := time.ParseDuration("24h")
				options.FileFollowDuration = d * 7
			}

			if len(w.PathPattern) > 0 {
				if r, err := regexp.Compile(w.PathPattern); err == nil {
					options.PathReg = r
				} else {
					logger.Errorln(err)
				}
			}

			var lineReg *regexp.Regexp
			if len(w.LinePattern) > 0 {
				if r, err := regexp.Compile(w.LinePattern); err == nil {
					lineReg = r
				} else {
					logger.Errorln(err)
				}
			}

			var ignoreReg *regexp.Regexp
			if len(w.LineIgnorePattern) > 0 {
				if r, err := regexp.Compile(w.LineIgnorePattern); err == nil {
					ignoreReg = r
				} else {
					logger.Errorln(err)
				}
			}

			var trails []*eye.Trail
			for _, directory := range w.Paths {
				if watcher, err := eye.NewDirectoryWatcher(directory); err == nil {
					// Create the new instance of the trail and begin following it.
					trail := eye.NewTrailWithOptions(watcher, options)

					if err = trail.Follow(getHandler(c, outLog, lineReg, ignoreReg, w)); err == nil {
						trails = append(trails, trail)
					} else {
						logger.Errorln(err)
						return
					}

					go func() {
						trail.AddUnfollower()
					}()
				} else {
					logger.Errorln(err)
					return
				}
			}

			// Wait for an interrupt or kill signal.
			signalChan := make(chan os.Signal, 1)
			signal.Notify(signalChan, os.Interrupt)
			go func() {
				for sig := range signalChan {
					if sig == os.Interrupt || sig == os.Kill {
						for _, trail := range trails {
							trail.End()
						}
						done <- true
					}
				}
			}()
		} else {
			logger.Errorln(err)
			return
		}
	}
	<-done
}

func setLogger(conf Config) {
	// Decide whether to output logs.
	logger = logrus.New()
	logger.Level.UnmarshalText([]byte(conf.LogLevel))
	logger.SetOutput(ioutil.Discard)
	if len(conf.Log) > 0 {
		if f, err := os.OpenFile(conf.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			logger.SetOutput(f)
		} else {
			logger.Errorln(err)
		}
	}
	if b, err := json.Marshal(conf); err == nil {
		fmt.Println("config: " + string(b))
	}
}

func setConfig(c *cli.Context) (Config, bool) {
	var conf Config
	if _, err := toml.DecodeFile(c.String("conf"), &conf); err != nil {
		logger.Errorln(err)
		return conf, false
	}

	if c.IsSet("pool") {
		conf.Pool = c.Bool("pool")
	}

	if len(conf.LogLevel) == 0 {
		conf.LogLevel = "info"
	}

	return conf, true
}

// getHandler builds the handler function to be used while following a trail.
func getHandler(c *cli.Context, outLog *os.File, lineReg *regexp.Regexp, ignoreReg *regexp.Regexp, w watch) eye.LineHandler {
	return func(line eye.Line) error {
		if ignoreReg != nil && ignoreReg.MatchString(line.Text) {
			return nil
		}

		output := ""

		if c.BoolT("prefix-path") {
			output += "[" + line.Path + "] "
		}

		if c.Bool("prefix-time") {
			output += "[" + line.Time.Format("Jan 2, 2006 at 3:04pm (MST)") + "] "
		}

		if w.Desc != "" {
			output += "[" + w.Desc + "] "
		}

		if lineReg != nil {
			if lineReg.MatchString(line.Text) {
				write(output, line, outLog)
			}
		} else {
			write(output, line, outLog)
		}

		return nil
	}
}

func write(output string, line eye.Line, outLog *os.File) {
	output += line.Text

	if _, err := outLog.WriteString(output + "\n"); err != nil {
		logger.Errorln(err)
	}
}

func writePidFile(c *cli.Context) error {
	var pidFile string
	if dir, err := filepath.Abs(filepath.Dir(os.Args[0])); err == nil {
		pidFile = filepath.Join(dir, "sauron.pid")
	}

	// Read in the pid file as a slice of bytes.
	if piddata, err := ioutil.ReadFile(pidFile); err == nil {
		// Convert the file contents to an integer.
		if pid, err := strconv.Atoi(string(piddata)); err == nil {
			// Look for the pid in the process list.
			if process, err := os.FindProcess(pid); err == nil {
				// Send the process a signal zero kill.
				if err := process.Signal(syscall.Signal(0)); err == nil {
					// We only get an error if the pid isn't running, or it's not ours.
					return fmt.Errorf("pid already running: %d", pid)
				}
			}
		}
	}

	// If we get here, then the pidfile didn't exist,
	// or the pid in it doesn't belong to the user running this app.
	return ioutil.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0664)
}

func task() {
	logger.Debugln("task running...")

}

func printMemUsage() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	logger.Debugf("Alloc = %v byte\n", m.Alloc)
	logger.Debugf("TotalAlloc = %v byte\n", m.TotalAlloc)
	logger.Debugf("Sys = %v MiB\n", bToMb(m.Sys))
	logger.Debugf("NumGC = %v\n", m.NumGC)
	logger.Debugf("Frees = %v\n", m.Frees)
}

func bToMb(b uint64) uint64 {
	return b / 1024 / 1024
}
