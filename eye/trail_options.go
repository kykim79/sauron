package eye

import "regexp"
import "time"
import "github.com/Sirupsen/logrus"

// SimpleLogger should be able to handle error and info log messages.
type SimpleLogger interface {
	Infoln(v ...interface{})
	Errorln(v ...interface{})
}

// TrailOptions are the different options supported by the Trail object.
type TrailOptions struct {
	// Logger to be used by the trail. Messages about file events and error
	// will be sent to this logger. Use ioutil.Discard to ignore output.
	//Logger SimpleLogger
	Logger *logrus.Logger

	// PollChanges dictates whether the Trail should continuosly poll for
	// chastes instead of using fsnotify.
	PollChanges bool

	// File Regex to follow. if nil then ignore
	FileReg *regexp.Regexp

	// Ignore File Regex to follow.
	FileIgnoreReg *regexp.Regexp

	// Ignore If File Mod time is order than duration.
	FileIgnoreDuration time.Duration

	// Unfollow If File Mod time is order than duration.
	FileFollowDuration time.Duration

	// Path Regex to follow.
	PathReg *regexp.Regexp
}
