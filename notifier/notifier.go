// The notifier package provides a way to communicate errors to external services.  The only supported service is Sentry.
package notifier

import (
	"time"

	"github.com/getsentry/raven-go"
	"github.com/golang/glog"
	"github.com/pkg/errors"
)

type ClientInterface interface {
	Notify(string, error) error
}

func NewClient(namespace, release string) *client {
	raven.SetEnvironment(namespace)
	raven.SetRelease(release)

	return &client{}
}

type client struct {
}

// Notify will log to stdout and post error and message to Sentry.
//
// TODO: Evaluate if we still need retry logic for Sentry
func (c *client) Notify(msg string, err error) error {
	glog.Info(errors.Wrap(err, msg))

	var nErr error
	var attempts int
	for attempts < 3 {
		if nErr = c.notifySentry(msg, err); nErr == nil {
			return nil
		}

		attempts += 1
		time.Sleep(1 * time.Second)
	}

	glog.Info(errors.Wrap(nErr, "Unable to POST to Sentry"))
	return nErr
}

func (c *client) notifySentry(msg string, err error) error {
	msgID := raven.CaptureErrorAndWait(err, map[string]string{"message": msg})
	if msgID == "" {
		return errors.New("Posting to Sentry failed")
	}

	return nil
}
