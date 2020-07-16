// Package eye provides the internals behind the Sauron CLI tool.
package eye

import (
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/hpcloud/tail"
	"github.com/jasonlvhit/gocron"
	fsnotify "gopkg.in/fsnotify.v1"
)

// Line contains a log line of a log file.
type Line struct {
	Path string
	Text string
	Time time.Time
	Err  error
}

// LineHandler is a function capable to handle log lines.
type LineHandler func(line Line) error

// Trail represents a log trail that can be followed for new lines. In
// conjunction with a Watcher, a Trail is capable of monitoring existing and new
// files in a directory.
//
// However, unlike the Watcher, a Trail is limited to traditional filesystems.
type Trail struct {
	watcher Watcher
	done    chan bool
	tails   []*tail.Tail
	options *TrailOptions
}

// NewTrail creates a new instance of a Trail.
func NewTrail(watcher Watcher) *Trail {
	return &Trail{
		watcher: watcher,
		done:    make(chan bool),
		options: &TrailOptions{
			Logger: logrus.New(),
		},
	}
}

// NewTrailWithOptions creates a new instance of a Trail with a custom set of
// options. If any option provided is nil, it will be replaced with a safe
// default.
func NewTrailWithOptions(watcher Watcher, options *TrailOptions) *Trail {
	// Create a default set of options.
	defaults := &TrailOptions{
		Logger:             logrus.New(),
		PollChanges:        options.PollChanges,
		FileReg:            options.FileReg,
		FileIgnoreReg:      options.FileIgnoreReg,
		FileIgnoreDuration: options.FileIgnoreDuration,
		FileFollowDuration: options.FileFollowDuration,
		PathReg:            options.PathReg,
	}

	// Replace the logger if an alternative is provided.
	if options.Logger != nil {
		defaults.Logger = options.Logger
	}

	return &Trail{
		watcher: watcher,
		done:    make(chan bool),
		options: defaults,
	}
}
func task(t *Trail) {
	t.options.Logger.Debugln("task running...")
	t.unfollowOldFiles()
}

func (t *Trail) AddUnfollower() {
	t.options.Logger.Infoln("added Old File Unfollower.")
	t.options.Logger.Infoln("File Follow Duration: " + t.options.FileFollowDuration.String())

	s := gocron.NewScheduler()
	s.Every(10).Seconds().Do(task, t)
	<-s.Start()
}

// Follow starts following a trail. Every time a file is changed, the affected
// lines will be passed to the handler function to be proccessed. The handler
// function could do something as simple as writing the lines that standard
// output, or do more advanced things like writing to an external log server.
func (t *Trail) Follow(handler LineHandler) error {
	t.options.Logger.Infoln("Sauron is now watching")

	// First, we tail all the files that we already know.
	files, err := t.watcher.Walk()

	if err != nil {
		t.options.Logger.Errorln("Failed to walk directory")

		return err
	}

	for _, file := range files {
		if ignore(t, file) {
			continue
		}

		t.followFile(file, handler, false)
	}

	// Second, we watch for new files, and tail them too.
	events := make(chan FileEvent)

	go func() {
		for {
			select {
			case event := <-events:
				if ignore(t, event.Path) {
					continue
				}

				switch event.Op {
				case fsnotify.Create:
					t.options.Logger.Debugln("Created: " + event.Path)
					t.followFile(event.Path, handler, true)
				case fsnotify.Remove:
					t.options.Logger.Debugln("Removed: " + event.Path)
					t.unfollowFile(event.Path)
				case fsnotify.Rename:
					t.options.Logger.Debugln("Renamed: " + event.Path)
				case fsnotify.Write:
					t.options.Logger.Debugln("Write: " + event.Path)
				default:
					t.options.Logger.Debugln(
						"Event " + strconv.Itoa(int(event.Op)) + ": " + event.Path,
					)
				}
			case <-t.done:
				// Stop the watcher
				t.watcher.End()

				// Stop any tailers
				for _, current := range t.tails {
					current.Stop()
				}

				// Exit the goroutine
				return
			}
		}
	}()

	t.watcher.Watch(events)

	return nil
}

func (t *Trail) isOldToIgnore(path string) bool {
	var result bool
	if info, err := os.Stat(path); err == nil {
		result = time.Now().Sub(info.ModTime()) > t.options.FileIgnoreDuration
	} else {
		t.options.Logger.Errorln("failed to get file info. " + err.Error())
		result = false
	}
	return result
}

func ignore(t *Trail, path string) bool {
	return (t.options.PathReg != nil && !t.options.PathReg.MatchString(filepath.Dir(path))) ||
		(t.options.FileReg != nil && !t.options.FileReg.MatchString(filepath.Base(path))) ||
		(t.options.FileIgnoreReg != nil && t.options.FileIgnoreReg.MatchString(filepath.Base(path)) ||
			t.isOldToIgnore(path))
}

// End stops watching.
func (t *Trail) End() {
	t.options.Logger.Infoln("Stopping...")

	t.done <- true
}

// followFile simply setups the appropriate options for the tail library and
// starts tailing that file. It also repackages events as Line objects for the
// handler function. The isNew parameter tells the function whether the file
// was just created or it already existed when the trail started following.
func (t *Trail) followFile(path string, handler LineHandler, isNew bool) {
	t.options.Logger.Debugln("Following: " + path)

	if t.options.PollChanges {
		t.options.Logger.Debugln("Polling enabled")
	}

	go func() {
		var current *tail.Tail
		var err error

		if isNew {
			current, err = tail.TailFile(path, tail.Config{
				Follow: true,
				Logger: tail.DiscardingLogger,
				Poll:   t.options.PollChanges,
			})

			if err != nil {
				return
			}
		} else {
			current, err = tail.TailFile(path, tail.Config{
				Follow:   true,
				Location: &tail.SeekInfo{Offset: 0, Whence: 2},
				Logger:   tail.DiscardingLogger,
				Poll:     t.options.PollChanges,
			})

			if err != nil {
				return
			}
		}

		t.tails = append(t.tails, current)

		for line := range current.Lines {
			newLine := Line{
				Path: path,
				Text: line.Text,
				Time: line.Time,
				Err:  line.Err,
			}

			handler(newLine)
		}
	}()
}

func (t *Trail) unfollowFile(name string) error {
	for i, tail := range t.tails {
		if tail.Filename == name {
			tail.Stop()
			t.tails = append(t.tails[:i], t.tails[i:]...)
		}
	}
	return nil
}

func (t *Trail) isOlderThanADay(tm time.Time) bool {
	return time.Now().Sub(tm) > t.options.FileFollowDuration
	//d, _ := time.ParseDuration("1m")
	//return time.Now().Sub(tm) > d
}

func (t *Trail) unfollowOldFiles() error {
	t.options.Logger.Debugln("starting...unfollow old files")

	i := 0
	for i < len(t.tails) {
		if info, err := os.Stat(t.tails[i].Filename); err == nil {
			if t.isOlderThanADay(info.ModTime()) {
				t.options.Logger.Debugln("unfollow: " + info.Name())
				t.tails[i].Stop()
				copy(t.tails[i:], t.tails[i+1:])
				t.tails[len(t.tails)-1] = nil // or the zero value of T
				t.tails = t.tails[:len(t.tails)-1]
			} else {
				t.options.Logger.Debugln("follow: " + info.Name())
				i++
			}

		} else {
			t.options.Logger.Errorln("failed to get file info. " + err.Error())
		}
	}

	t.options.Logger.Debugln("unfollow completed. ")
	for _, tail := range t.tails {
		t.options.Logger.Debugln("following: " + tail.Filename)
	}
	return nil
}
