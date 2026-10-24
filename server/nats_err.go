package server

import (
	hk "github.com/Defolt-Labs/defolt-httpkit"
)

// natsConnectErrText hides a url.Error, whose text repeats the NATS URL with its password.
func natsConnectErrText(err error) string {
	return hk.URLError("NATS_URL", err).Error()
}
