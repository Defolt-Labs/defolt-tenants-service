package logger

import (
	"os"

	"github.com/sirupsen/logrus"
)

var base = logrus.New()

func init() {
	base.SetOutput(os.Stdout)
	base.SetFormatter(&logrus.JSONFormatter{TimestampFormat: "2006-01-02T15:04:05.000Z07:00"})
}

func LogInfo(scope, msg string) {
	base.WithField("scope", scope).Info(msg)
}
func LogWarn(reqID, scope, msg string) {
	base.WithFields(logrus.Fields{"scope": scope, "request_id": reqID}).Warn(msg)
}
func LogError(reqID, scope, msg string) {
	base.WithFields(logrus.Fields{"scope": scope, "request_id": reqID}).Error(msg)
}
