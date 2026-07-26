package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hyird/GuardDNS/internal/statewire"
	"github.com/miekg/dns"
)

const dohBridgeAddr = "127.0.0.1:5336"

type resolverFunc func(context.Context, *dns.Msg) (*dns.Msg, error)

type dohUpstream struct {
	tag    string
	url    string
	client *http.Client
}

type dohBridge struct {
	resolvers []resolverFunc
	log       *logger
	udp       *dns.Server
	tcp       *dns.Server
	closeOnce sync.Once
	warnMu    sync.Mutex
	lastWarn  time.Time
}

func startDoHBridge(ctx context.Context, state *runtimeState, log *logger) (*dohBridge, error) {
	bridge := &dohBridge{
		log: log,
	}
	for _, upstream := range defaultDoHUpstreams() {
		bridge.resolvers = append(bridge.resolvers, upstream.exchange)
	}

	udpConn, err := net.ListenPacket("udp4", dohBridgeAddr)
	if err != nil {
		return nil, fmt.Errorf("listen encrypted bridge UDP: %w", err)
	}
	tcpListener, err := net.Listen("tcp4", dohBridgeAddr)
	if err != nil {
		_ = udpConn.Close()
		return nil, fmt.Errorf("listen encrypted bridge TCP: %w", err)
	}
	bridge.udp = &dns.Server{PacketConn: udpConn, Handler: bridge}
	bridge.tcp = &dns.Server{Listener: tcpListener, Handler: bridge}
	state.update("doh_bridge", func(component *statewire.Component) {
		component.Up = true
	})

	serve := func(network string, server *dns.Server) {
		if err := server.ActivateAndServe(); err != nil && ctx.Err() == nil {
			log.errorf("encrypted bridge %s stopped: %v", network, err)
			state.update("doh_bridge", func(component *statewire.Component) {
				component.Up = false
			})
		}
	}
	go serve("UDP", bridge.udp)
	go serve("TCP", bridge.tcp)
	go func() {
		<-ctx.Done()
		bridge.close()
		state.update("doh_bridge", func(component *statewire.Component) {
			component.Up = false
		})
	}()
	log.infof("encrypted DoH bridge ready on %s", dohBridgeAddr)
	return bridge, nil
}

func defaultDoHUpstreams() []*dohUpstream {
	return []*dohUpstream{
		newDoHUpstream(
			"cloudflare",
			"https://cloudflare-dns.com/dns-query",
			"cloudflare-dns.com",
			[]string{"104.16.248.249", "104.16.249.249"},
		),
		newDoHUpstream(
			"360",
			"https://doh.360.cn/dns-query",
			"doh.360.cn",
			[]string{
				"101.226.4.6",
				"218.30.118.6",
				"101.91.111.153",
				"36.99.170.86",
				"106.63.24.74",
			},
		),
	}
}

func newDoHUpstream(tag, url, serverName string, dialIPs []string) *dohUpstream {
	var next atomic.Uint64
	dialer := &net.Dialer{Timeout: 2 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:               nil,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        8,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: serverName,
		},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			if len(dialIPs) == 0 {
				return nil, errors.New("encrypted upstream has no dial address")
			}
			index := int(next.Add(1)-1) % len(dialIPs)
			return dialer.DialContext(ctx, network, net.JoinHostPort(dialIPs[index], "443"))
		},
	}
	return &dohUpstream{
		tag: tag,
		url: url,
		client: &http.Client{
			Transport: transport,
			Timeout:   4 * time.Second,
		},
	}
}

func (u *dohUpstream) exchange(ctx context.Context, query *dns.Msg) (*dns.Msg, error) {
	payload, err := query.Pack()
	if err != nil {
		return nil, fmt.Errorf("%s pack query: %w", u.tag, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, u.url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("%s create request: %w", u.tag, err)
	}
	request.Header.Set("Accept", "application/dns-message")
	request.Header.Set("Content-Type", "application/dns-message")
	response, err := u.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s request: %w", u.tag, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned HTTP %d", u.tag, response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, dns.MaxMsgSize))
	if err != nil {
		return nil, fmt.Errorf("%s read response: %w", u.tag, err)
	}
	answer := new(dns.Msg)
	if err := answer.Unpack(body); err != nil {
		return nil, fmt.Errorf("%s decode response: %w", u.tag, err)
	}
	if !answer.Response || len(answer.Question) != len(query.Question) {
		return nil, fmt.Errorf("%s returned an invalid DNS response", u.tag)
	}
	answer.Id = query.Id
	return answer, nil
}

func (b *dohBridge) ServeDNS(writer dns.ResponseWriter, query *dns.Msg) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		response *dns.Msg
		err      error
	}
	results := make(chan result, len(b.resolvers))
	for _, resolver := range b.resolvers {
		resolver := resolver
		go func() {
			response, err := resolver(ctx, query.Copy())
			results <- result{response: response, err: err}
		}()
	}

	var lastErr error
	for range b.resolvers {
		select {
		case <-ctx.Done():
			lastErr = ctx.Err()
		case outcome := <-results:
			if outcome.err == nil && outcome.response != nil {
				cancel()
				_ = writer.WriteMsg(outcome.response)
				return
			}
			lastErr = outcome.err
		}
	}
	b.warnUnavailable(lastErr)
	failure := new(dns.Msg)
	failure.SetRcode(query, dns.RcodeServerFailure)
	_ = writer.WriteMsg(failure)
}

func (b *dohBridge) warnUnavailable(err error) {
	b.warnMu.Lock()
	defer b.warnMu.Unlock()
	if time.Since(b.lastWarn) < 30*time.Second {
		return
	}
	b.lastWarn = time.Now()
	b.log.warnf("all encrypted DNS upstreams failed: %v", err)
}

func (b *dohBridge) close() {
	b.closeOnce.Do(func() {
		if b.udp != nil {
			_ = b.udp.Shutdown()
		}
		if b.tcp != nil {
			_ = b.tcp.Shutdown()
		}
	})
}
