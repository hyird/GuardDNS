package cancelclassify

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
)

func TestExpected(t *testing.T) {
	for _, err := range []error{
		context.Canceled,
		fmt.Errorf("query failed: %w", context.Canceled),
		errors.New("connection ctx canceled"),
		errors.New("write tcp: use of closed network connection"),
		errors.New("write: broken pipe"),
		net.ErrClosed,
	} {
		if !Expected(err) {
			t.Errorf("Expected(%q) = false", err)
		}
	}
	if Expected(context.DeadlineExceeded) {
		t.Fatal("deadline exceeded was classified as a client cancellation")
	}
	if Expected(errors.New("upstream returned SERVFAIL")) {
		t.Fatal("upstream failure was classified as a client cancellation")
	}
}
