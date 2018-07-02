package etcdhcp

import (
	"reflect"
	"testing"
	"time"

	"github.com/mholt/caddy"
)

func TestSetupEtcdhcp(t *testing.T) {
	type testcase struct {
		input              string
		shouldErr          bool
		expectedZones      []string
		expectedEndpoint   []string
		expectedEtcdPrefix string
		expectedTTL        time.Duration
	}

	testcases := []testcase{
		{
			`etcdhcp`, false, []string{"."}, []string{"http://localhost:2379"}, "etcdhcp::", 5 * time.Minute,
		},
		{
			`etcdhcp foobar.etcdhcp.local alias.local {
	endpoint http://192.168.0.101:2379 http://192.168.0.102:2379 http://192.168.0.103:2379
	prefix etcdhcp::foobar::
	ttl 1h
}`, false, []string{"foobar.etcdhcp.local.", "alias.local."}, []string{"http://192.168.0.101:2379", "http://192.168.0.102:2379", "http://192.168.0.103:2379"}, "etcdhcp::foobar::", 1 * time.Hour,
		},
		{
			`etcdhcp foobar.etcdhcp.local {
	endpoints http://192.168.0.101:2379 http://192.168.0.102:2379 http://192.168.0.103:2379
	prefix etcdhcp::foobar::
	ttl 1h
}`, true, []string{}, []string{}, "", 0,
		},
	}

	for i, testcase := range testcases {
		c := caddy.NewTestController("dns", testcase.input)
		c.ServerBlockKeys = []string{"."}

		etcdhcp, err := etcdhcpParse(c)
		if testcase.shouldErr {
			if err == nil {
				t.Errorf("Test %d: No error has been found while it was expected for input `%s`", i, testcase.input)
			}
			continue
		}
		if !testcase.shouldErr && err != nil {
			t.Errorf("Test %d: An error '%v' has been found while it was not expected for input `%s`", i, err, testcase.input)
			continue
		}
		if !reflect.DeepEqual(etcdhcp.Zones, testcase.expectedZones) {
			t.Errorf("Test %d: Actual zone = '%s' differs from expected zone = '%s'", i, etcdhcp.Zones, testcase.expectedZones)
		}
		if !reflect.DeepEqual(etcdhcp.Endpoint, testcase.expectedEndpoint) {
			t.Errorf("Test %d: Actual endpoint = '%v' differs from expected endpoint = '%v'", i, etcdhcp.Endpoint, testcase.expectedEndpoint)
		}
		if etcdhcp.EtcdPrefix != testcase.expectedEtcdPrefix {
			t.Errorf("Test %d: Actual etcd prefix = '%s' differs from expected etcd prefix = '%s'", i, etcdhcp.EtcdPrefix, testcase.expectedEtcdPrefix)
		}
		if etcdhcp.TTL != testcase.expectedTTL {
			t.Errorf("Test %d: Actual TTL = '%v' differs from expected TTL = '%v'", i, etcdhcp.TTL, testcase.expectedTTL)
		}

	}

}
