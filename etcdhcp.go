package etcdhcp

import (
	"context"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/miekg/dns"
)

type Etcdhcp struct {
	Next       plugin.Handler
	Zones      []string
	Endpoint   []string
	EtcdPrefix string
	TTL        time.Duration
}

func (e *Etcdhcp) ServeDNS(context.Context, dns.ResponseWriter, *dns.Msg) (int, error) {
	// TODO: Not implemented
	return 0, nil
}

func (e *Etcdhcp) Name() string {
	return "etcdhcp"
}
