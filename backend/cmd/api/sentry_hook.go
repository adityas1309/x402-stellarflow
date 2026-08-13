package main

import (
	"github.com/getsentry/sentry-go"
	"github.com/sirupsen/logrus"
)

// sentryLogrusHook forwards logrus error/fatal/panic entries to Sentry as events,
// preserving fields as Sentry tags/extra data.
type sentryLogrusHook struct{}

func newSentryLogrusHook() *sentryLogrusHook {
	return &sentryLogrusHook{}
}

func (h *sentryLogrusHook) Levels() []logrus.Level {
	return []logrus.Level{
		logrus.PanicLevel,
		logrus.FatalLevel,
		logrus.ErrorLevel,
	}
}

func (h *sentryLogrusHook) Fire(entry *logrus.Entry) error {
	hub := sentry.CurrentHub().Clone()
	hub.WithScope(func(scope *sentry.Scope) {
		// Map level
		switch entry.Level {
		case logrus.PanicLevel, logrus.FatalLevel:
			scope.SetLevel(sentry.LevelFatal)
		case logrus.ErrorLevel:
			scope.SetLevel(sentry.LevelError)
		default:
			scope.SetLevel(sentry.LevelInfo)
		}

		// Attach all logrus fields as extras
		for k, v := range entry.Data {
			scope.SetExtra(k, v)
		}

		// If there's an "error" field, capture it as the actual error so Sentry can group by it
		if errVal, ok := entry.Data[logrus.ErrorKey]; ok {
			if err, ok := errVal.(error); ok {
				hub.CaptureException(err)
				return
			}
		}
		// Otherwise capture the log message
		hub.CaptureMessage(entry.Message)
	})
	return nil
}
