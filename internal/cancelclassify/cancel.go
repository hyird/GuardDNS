package cancelclassify

import (
	"context"
	"errors"
	"net"
	"strings"
)

var expectedFragments = []string{
	"context canceled",
	"connection ctx canceled",
	"use of closed network connection",
	"broken pipe",
	"connection reset by peer",
}

// Expected reports whether an error was caused by a downstream client closing
// its TCP connection. These events are useful as counters, but they are not DNS
// resolution failures and should not pollute warning logs or err_total.
func Expected(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	message := strings.ToLower(err.Error())
	for _, fragment := range expectedFragments {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}
