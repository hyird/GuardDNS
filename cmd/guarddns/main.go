package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"
)

var version = "dev"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: guarddns [version]")
		os.Exit(2)
	}

	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "GuardDNS configuration error: %v\n", err)
		os.Exit(2)
	}
	log := newLogger(cfg.logLevel)
	if err := prepareRuntime(cfg); err != nil {
		log.errorf("runtime initialization failed: %v", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer stop()

	state := newRuntimeState()
	go sendStateLoop(ctx, state, supervisorSocket)

	var managers sync.WaitGroup
	managers.Add(1)
	go superviseChild(ctx, childSpec{
		name: "unbound",
		path: "/usr/sbin/unbound",
		args: []string{"-d", "-c", unboundRuntimeConfig},
	}, state, log, &managers)

	if err := waitForTCP(ctx, "127.0.0.1:5335", 3*time.Second); err != nil {
		log.warnf("Unbound is not ready; starting MosDNS in degraded mode: %v", err)
	}

	managers.Add(1)
	go superviseChild(ctx, childSpec{
		name: "mosdns",
		path: "/usr/local/bin/mosdns",
		args: []string{"start", "-c", mosdnsRuntimeConfig},
	}, state, log, &managers)

	mode := "secure"
	if cfg.autoEnabled {
		mode = "auto-forward"
	}
	if err := waitForTCP(ctx, "127.0.0.1:53", 5*time.Second); err != nil {
		log.warnf("MosDNS is not ready and will keep restarting: %v", err)
	} else {
		log.infof("ready mode=%s dns=:53 secure=:5304 metrics=:9091", mode)
	}

	<-ctx.Done()
	log.infof("shutdown requested")
	managers.Wait()
}
