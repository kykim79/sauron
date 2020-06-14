// Package console contains Sauron's CLI commands.
package console

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"

	"github.com/BurntSushi/toml"
	"github.com/Sirupsen/logrus"
	"github.com/etcinit/sauron/eye"
	"gopkg.in/urfave/cli.v1"
)

type Config struct {
	Watch      []watch
	Log        string // sauron log
	Pool       bool   // poll for changes instead of using fsnotify (for tailing)
	Verbose    bool
	PrefixTime bool // prefix time to every output line
	PrefixPath bool // prefix file path to every output line (default)
}

type watch struct {
	Paths       []string
	ExtPattern  string // file extensions to watch
	LinePattern string // pattern string to match
	Out         string // file to write output line
	Desc        string
}

// MainAction is the main action executed when using Sauron.
func MainAction(c *cli.Context) {

	var conf Config
	if _, err := toml.DecodeFile(c.String("conf"), &conf); err != nil {
		logrus.Errorln(err)
		return
	}

	if len(conf.Log) > 0 {
		if f, err := os.OpenFile(conf.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			logrus.SetOutput(f)
			defer f.Close()
		} else {
			logrus.Errorln(err)
		}
	}

	conf.Verbose = c.Bool("verbose")

	writePidFile(c)

	done := make(chan bool)

	options := &eye.TrailOptions{
		PollChanges: conf.Pool,
	}

	// Decide whether to output logs.
	if conf.Verbose {
		options.Logger = logrus.New()
	} else {
		log := logrus.New()
		log.Out = ioutil.Discard
		options.Logger = log
	}

	for _, w := range conf.Watch {
		if outLog, err := os.OpenFile(w.Out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
			var extReg *regexp.Regexp
			if len(w.ExtPattern) > 0 {
				if r, err := regexp.Compile(w.ExtPattern); err == nil {
					extReg = r
				} else {
					logrus.Errorln(err)
				}
			}

			var lineReg *regexp.Regexp
			if len(w.LinePattern) > 0 {
				if r, err := regexp.Compile(w.LinePattern); err == nil {
					lineReg = r
				} else {
					logrus.Errorln(err)
				}
			}

			var trails []*eye.Trail
			for _, directory := range w.Paths {
				if watcher, err := eye.NewDirectoryWatcher(directory); err == nil {
					// Create the new instance of the trail and begin following it.
					trail := eye.NewTrailWithOptions(watcher, options)
					if err = trail.Follow(getHandler(c, outLog, extReg, lineReg, w)); err == nil {
						trails = append(trails, trail)
					} else {
						logrus.Errorln(err)
						return
					}
				} else {
					logrus.Errorln(err)
					return
				}
			}
			defer outLog.Close()

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
			logrus.Errorln(err)
			return
		}
	}
	<-done
}

func contains(s []string, substr string) bool {
	for _, v := range s {
		if v == substr {
			return true
		}
	}
	return false
}

// getHandler builds the handler function to be used while following a trail.
func getHandler(c *cli.Context, outLog *os.File, extReg *regexp.Regexp, lineReg *regexp.Regexp, w watch) eye.LineHandler {
	return func(line eye.Line) error {

		if extReg != nil && !extReg.MatchString(filepath.Ext(line.Path)) {
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

		if lineReg != nil && lineReg.MatchString(line.Text) {
			write(output, line, outLog)
		} else {
			write(output, line, outLog)
		}

		return nil
	}
}

func write(output string, line eye.Line, outLog *os.File) {
	output += line.Text
	fmt.Println(output)
	if _, err := outLog.WriteString(output + "\n"); err != nil {
		logrus.Errorln(err)
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
