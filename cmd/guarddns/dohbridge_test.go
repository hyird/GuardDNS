package main

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/miekg/dns"
)

func TestDoHBridgeReturnsFirstSuccessfulResponse(t *testing.T) {
	query := new(dns.Msg)
	query.SetQuestion("example.com.", dns.TypeA)
	bridge := &dohBridge{
		log: newLogger("error"),
		resolvers: []resolverFunc{
			func(context.Context, *dns.Msg) (*dns.Msg, error) {
				return nil, errors.New("unavailable")
			},
			func(_ context.Context, request *dns.Msg) (*dns.Msg, error) {
				response := new(dns.Msg)
				response.SetReply(request)
				response.Answer = append(response.Answer, &dns.A{
					Hdr: dns.RR_Header{
						Name:   request.Question[0].Name,
						Rrtype: dns.TypeA,
						Class:  dns.ClassINET,
						Ttl:    60,
					},
					A: net.ParseIP("192.0.2.1"),
				})
				return response, nil
			},
		},
	}
	writer := newCaptureDNSWriter()
	bridge.ServeDNS(writer, query)
	if writer.response == nil || writer.response.Rcode != dns.RcodeSuccess {
		t.Fatalf("response = %#v", writer.response)
	}
	if len(writer.response.Answer) != 1 {
		t.Fatalf("answer count = %d", len(writer.response.Answer))
	}
}

func TestDoHBridgeReturnsServfailWhenAllUpstreamsFail(t *testing.T) {
	query := new(dns.Msg)
	query.SetQuestion("example.com.", dns.TypeA)
	bridge := &dohBridge{
		log: newLogger("error"),
		resolvers: []resolverFunc{
			func(context.Context, *dns.Msg) (*dns.Msg, error) {
				return nil, errors.New("unavailable")
			},
		},
	}
	writer := newCaptureDNSWriter()
	bridge.ServeDNS(writer, query)
	if writer.response == nil || writer.response.Rcode != dns.RcodeServerFailure {
		t.Fatalf("response = %#v", writer.response)
	}
}

type captureDNSWriter struct {
	response *dns.Msg
}

func newCaptureDNSWriter() *captureDNSWriter {
	return new(captureDNSWriter)
}

func (w *captureDNSWriter) LocalAddr() net.Addr  { return &net.UDPAddr{} }
func (w *captureDNSWriter) RemoteAddr() net.Addr { return &net.UDPAddr{} }
func (w *captureDNSWriter) WriteMsg(message *dns.Msg) error {
	w.response = message.Copy()
	return nil
}
func (w *captureDNSWriter) Write(payload []byte) (int, error) {
	message := new(dns.Msg)
	if err := message.Unpack(payload); err != nil {
		return 0, err
	}
	w.response = message
	return len(payload), nil
}
func (w *captureDNSWriter) Close() error        { return nil }
func (w *captureDNSWriter) TsigStatus() error   { return nil }
func (w *captureDNSWriter) TsigTimersOnly(bool) {}
func (w *captureDNSWriter) Hijack()             {}
