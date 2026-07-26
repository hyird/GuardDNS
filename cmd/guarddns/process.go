package main

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/hyird/GuardDNS/internal/statewire"
)

const (
	restartInitial = time.Second
	restartMaximum = 30 * time.Second
	stableRuntime  = 5 * time.Minute
	shutdownGrace  = 5 * time.Second
)

type childSpec struct {
	name string
	path string
	args []string
}

func superviseChild(
	ctx context.Context,
	spec childSpec,
	state *runtimeState,
	log *logger,
	wg *sync.WaitGroup,
) {
	defer wg.Done()
	var step uint

	for {
		if ctx.Err() != nil {
			return
		}
		cmd := exec.Command(spec.path, spec.args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		configureProcess(cmd)
		startedAt := time.Now()
		if err := cmd.Start(); err != nil {
			log.errorf("%s failed to start: %v", spec.name, err)
			state.update(spec.name, func(component *statewire.Component) {
				component.Up = false
			})
		} else {
			state.update(spec.name, func(component *statewire.Component) {
				component.Up = true
				component.BackoffSeconds = 0
			})
			log.infof("%s started pid=%d", spec.name, cmd.Process.Pid)

			waited := make(chan error, 1)
			go func() { waited <- cmd.Wait() }()
			select {
			case err := <-waited:
				state.update(spec.name, func(component *statewire.Component) {
					component.Up = false
				})
				if ctx.Err() != nil {
					return
				}
				log.warnf("%s exited after %s: %v", spec.name, time.Since(startedAt).Round(time.Millisecond), err)
			case <-ctx.Done():
				terminateProcess(cmd)
				select {
				case <-waited:
				case <-time.After(shutdownGrace):
					killProcess(cmd)
					<-waited
				}
				state.update(spec.name, func(component *statewire.Component) {
					component.Up = false
				})
				return
			}
		}

		if time.Since(startedAt) >= stableRuntime {
			step = 0
		}
		step++
		delay := restartDelay(step)
		state.update(spec.name, func(component *statewire.Component) {
			component.Up = false
			component.Restarts++
			component.BackoffSeconds = delay.Seconds()
		})
		log.warnf("%s restart scheduled in %s", spec.name, delay.Round(time.Millisecond))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func restartDelay(step uint) time.Duration {
	nominal := restartInitial
	for i := uint(1); i < step && nominal < restartMaximum; i++ {
		nominal *= 2
		if nominal >= restartMaximum {
			nominal = restartMaximum
			break
		}
	}
	spread := nominal / 5
	delay := nominal - spread + time.Duration(rand.Int63n(int64(2*spread)+1))
	if delay > restartMaximum {
		return restartMaximum
	}
	return delay
}

func waitForTCP(ctx context.Context, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("%s did not become ready within %s", address, timeout)
}
