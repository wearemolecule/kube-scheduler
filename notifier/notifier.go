// The notifier package provides a way to communicate errors to external services.  The only supported service is Honeybader.
package notifier

import (
	"time"

	"github.com/golang/glog"
	honeybadger "github.com/honeybadger-io/honeybadger-go"
	"github.com/pkg/errors"
)

type ClientInterface interface {
	Notify(string, error) error
}

func NewClient(namespace, apiKey string) *client {
	var usingHoneybadger bool
	if apiKey != "" {
		honeybadger.Configure(
			honeybadger.Configuration{
				Env:    namespace,
				APIKey: apiKey,
			},
		)
		usingHoneybadger = true
	}

	return &client{namespace, usingHoneybadger}
}

type client struct {
	namespace        string
	usingHoneybadger bool
}

// Notify will log to stdout and post error and message to honeybadger.
//
// We have experienced timeout issues when trying to post to honeybadger,
// to help fix that we will retry attempts 3 times before logging and returning the error.
func (c *client) Notify(msg string, err error) error {
	glog.Info(errors.Wrap(err, msg))

	var nErr error
	var attempts int
	for attempts < 3 {
		if nErr = c.notifyHoneybadger(msg, err); nErr == nil {
			return nil
		}

		attempts += 1
		time.Sleep(1 * time.Second)
	}

	glog.Info(errors.Wrap(nErr, "Unable to POST to honeybadger"))
	return nErr
}

func (c *client) notifyHoneybadger(msg string, err error) error {
	if !c.usingHoneybadger {
		return nil
	}

	_, nErr := honeybadger.Notify(msg, honeybadger.Context{"error": err}, honeybadger.Fingerprint{time.Now().String()})
	return nErr
}
