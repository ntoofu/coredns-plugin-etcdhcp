package etcdhcp

import (
	"context"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"

	etcd "github.com/coreos/etcd/clientv3"
)

// implement plugin.ServiceBackend and plugin.HandlerFunc
type Etcdhcp struct {
	Next       plugin.Handler
	Zones      []string
	Endpoint   []string
	EtcdPrefix string
	TTL        time.Duration
}

func (e *Etcdhcp) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r, Context: ctx}
	qname := state.Name()
	qtype := state.Type()

	zone := plugin.Zones(e.Zones).Matches(qname)
	if zone == "" || !strings.HasSuffix(qname, zone) {
		return plugin.NextOrFailure(qname, e.Next, ctx, w, r)
	}

	if qname == zone {
		switch qtype {
		case "A":
			nameservers, err := e.getNS(ctx)
			if err != nil {
				return dns.RcodeSuccess, err
			}
			e.sendSoaRecord(&state, nameservers, dns.RcodeSuccess) // NoData
		case "NS":
			nameservers, err := e.getNS(ctx)
			if err != nil {
				return dns.RcodeSuccess, err
			}
			e.sendNSRecords(&state, zone, nameservers)
		default:
			return dns.RcodeNotImplemented, nil
		}
		return dns.RcodeSuccess, nil
	}

	hostname := qname[0 : len(qname)-len(zone)-1]
	switch qtype {
	case "A":
		ipaddrs, err := e.getIPAddr(ctx, hostname)
		if err != nil {
			return dns.RcodeSuccess, err
		}
		if len(ipaddrs) > 0 {
			e.sendARecords(&state, ipaddrs)
		} else {
			nameservers, err := e.getNS(ctx)
			if err != nil {
				return dns.RcodeSuccess, err
			}
			e.sendSoaRecord(&state, nameservers, dns.RcodeNameError) // NXDomain
		}
	case "NS":
		nameservers, err := e.getNS(ctx)
		if err != nil {
			return dns.RcodeSuccess, err
		}
		e.sendSoaRecord(&state, nameservers, dns.RcodeNameError) // NXDomain
	default:
		return dns.RcodeNotImplemented, nil
	}

	return dns.RcodeSuccess, nil
}

func (e *Etcdhcp) Name() string {
	return "etcdhcp"
}

func (e *Etcdhcp) getIPAddr(ctx context.Context, hostname string) ([]net.IP, error) {
	c, err := etcd.New(etcd.Config{Endpoints: e.Endpoint})
	if err != nil {
		return nil, err
	}
	defer c.Close()

	kvc := etcd.NewKV(c)
	ipaddrs := make([]net.IP, 0)
	nameserverKvKey := e.EtcdPrefix + "coredns::ns::" + hostname
	staticRecordKvKey := e.EtcdPrefix + "coredns::ip::" + hostname
	for _, kvKey := range []string{nameserverKvKey, staticRecordKvKey} {
		resp, err := kvc.Get(ctx, kvKey)
		if err != nil {
			return nil, err
		}
		if len(resp.Kvs) > 0 {
			val := resp.Kvs[0].Value
			ipaddr := net.ParseIP(string(val))
			if ipaddr != nil {
				return []net.IP{ipaddr}, nil
			}
		}
	}

	nicBindingKeyPref := e.EtcdPrefix + "coredns::nic::" + hostname + "::"
	resp, err := kvc.Get(ctx, nicBindingKeyPref, etcd.WithPrefix())
	if err != nil {
		return nil, err
	}

	if len(resp.Kvs) == 0 {
		dhcpKeyPref := e.EtcdPrefix + "hostnames::nic::" + hostname + "::"
		resp, err = kvc.Get(ctx, dhcpKeyPref, etcd.WithPrefix())
		if err != nil {
			return nil, err
		}
	}

	for _, kv := range resp.Kvs {
		nic := string(kv.Value)
		key := e.EtcdPrefix + "nics::leased::" + nic
		resp, err := kvc.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		for _, kv := range resp.Kvs {
			ipaddr := net.ParseIP(string(kv.Value))
			if ipaddr != nil {
				ipaddrs = append(ipaddrs, ipaddr)
			}
		}
	}

	return ipaddrs, nil
}

func (e *Etcdhcp) getNS(ctx context.Context) (*map[string]net.IP, error) {
	c, err := etcd.New(etcd.Config{Endpoints: e.Endpoint})
	if err != nil {
		return nil, err
	}
	defer c.Close()

	kvc := etcd.NewKV(c)
	keyPref := e.EtcdPrefix + "coredns::ns::"
	resp, err := kvc.Get(ctx, keyPref, etcd.WithPrefix())
	if err != nil {
		return nil, err
	}
	nameservers := map[string]net.IP{}
	for _, kv := range resp.Kvs {
		key := string(kv.Key)
		val := string(kv.Value)
		nameservers[key[len(keyPref):]] = net.ParseIP(val)
	}

	return &nameservers, nil
}

func (e *Etcdhcp) sendARecords(state *request.Request, ipaddrs []net.IP) {
	msg := new(dns.Msg)
	msg.SetReply(state.Req)
	msg.Authoritative, msg.RecursionAvailable, msg.Compress = true, false, true
	for _, ipaddr := range ipaddrs {
		answer := new(dns.A)
		answer.Hdr = dns.RR_Header{
			Name:   state.Name(),
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    uint32(e.TTL.Seconds()),
		}
		answer.A = ipaddr
		msg.Answer = append(msg.Answer, answer)
	}
	state.SizeAndDo(msg)
	msg, _ = state.Scrub(msg)
	state.W.WriteMsg(msg)
	return
}

func (e *Etcdhcp) sendNSRecords(state *request.Request, zone string, nameservers *map[string]net.IP) {
	msg := new(dns.Msg)
	msg.SetReply(state.Req)
	msg.Authoritative, msg.RecursionAvailable, msg.Compress = true, false, true
	for nsHost, nsIP := range *nameservers {
		// Answer NS records
		answer := new(dns.NS)
		answer.Hdr = dns.RR_Header{
			Name:   state.Name(),
			Rrtype: dns.TypeNS,
			Class:  dns.ClassINET,
			Ttl:    uint32(e.TTL.Seconds()),
		}
		hostname := nsHost + "." + zone
		answer.Ns = hostname
		msg.Answer = append(msg.Answer, answer)
		// Add glue records
		glue := new(dns.A)
		glue.Hdr = dns.RR_Header{
			Name:   hostname,
			Rrtype: dns.TypeA,
			Class:  dns.ClassINET,
			Ttl:    uint32(e.TTL.Seconds()),
		}
		glue.A = nsIP
		msg.Extra = append(msg.Extra, glue)
	}
	state.SizeAndDo(msg)
	msg, _ = state.Scrub(msg)
	state.W.WriteMsg(msg)
	return
}

func (e *Etcdhcp) sendSoaRecord(state *request.Request, nameservers *map[string]net.IP, rcode int) {
	nsNames := make([]string, 0, len(*nameservers))
	for name, _ := range *nameservers {
		nsNames = append(nsNames, name)
	}
	sort.Strings(nsNames)

	qname := state.Name()
	zone := plugin.Zones(e.Zones).Matches(qname)
	ttl := uint32(e.TTL.Seconds())
	msg := new(dns.Msg)
	msg.SetRcode(state.Req, rcode)
	msg.Authoritative, msg.RecursionAvailable, msg.Compress = true, false, true
	soaRecord := &dns.SOA{
		Hdr:     dns.RR_Header{Name: zone, Rrtype: dns.TypeSOA, Ttl: ttl, Class: dns.ClassINET},
		Mbox:    "postmaster." + zone,
		Ns:      nsNames[0] + "." + zone,
		Serial:  uint32(time.Now().Unix()),
		Refresh: 7200,
		Retry:   1800,
		Expire:  86400,
		Minttl:  ttl,
	}
	msg.Ns = []dns.RR{soaRecord}
	state.SizeAndDo(msg)
	msg, _ = state.Scrub(msg)
	state.W.WriteMsg(msg)
}
