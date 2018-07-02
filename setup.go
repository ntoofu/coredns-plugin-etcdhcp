package etcdhcp

import (
	"time"

	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/mholt/caddy"
)

func init() {
	caddy.RegisterPlugin("etcdhcp", caddy.Plugin{
		ServerType: "dns",
		Action:     setup,
	})
}

func setup(c *caddy.Controller) error {
	e, err := etcdhcpParse(c)
	// _, err := etcdhcpParse(c)
	if err != nil {
		return plugin.Error("etcdhcp", err)
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		e.Next = next
		return e
	})
	return nil
}

func etcdhcpParse(c *caddy.Controller) (*Etcdhcp, error) {
	e := Etcdhcp{
		Endpoint:   []string{"http://localhost:2379"},
		EtcdPrefix: "etcdhcp::",
		TTL:        5 * time.Minute,
	}

	for c.Next() {
		args := c.RemainingArgs()
		if len(args) == 0 {
			e.Zones = make([]string, len(c.ServerBlockKeys))
			copy(e.Zones, c.ServerBlockKeys)
		} else {
			e.Zones = args
		}
		for i, zone := range e.Zones {
			e.Zones[i] = plugin.Host(zone).Normalize()
		}

		if c.NextBlock() {
			for {
				switch c.Val() {
				case "endpoint":
					args := c.RemainingArgs()
					if len(args) == 0 {
						return &Etcdhcp{}, c.ArgErr()
					}
					e.Endpoint = args
				case "prefix":
					args := c.RemainingArgs()
					if len(args) != 1 {
						return &Etcdhcp{}, c.ArgErr()
					}
					e.EtcdPrefix = args[0]
				case "ttl":
					args := c.RemainingArgs()
					if len(args) != 1 {
						return &Etcdhcp{}, c.ArgErr()
					}
					ttl, err := time.ParseDuration(args[0])
					if err != nil {
						return &Etcdhcp{}, err
					}
					e.TTL = ttl
				default:
					if c.Val() != "}" {
						return &Etcdhcp{}, c.Errf("unknown propety '%s'", c.Val())
					}
				}
				if !c.Next() {
					break
				}
			}
		}
	}

	return &e, nil
}
