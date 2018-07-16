package etcdhcp

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"regexp"
	"testing"
	"time"

	etcd "github.com/coreos/etcd/clientv3"
	"github.com/coreos/etcd/embed"
	"github.com/miekg/dns"
	"github.com/pkg/errors"
)

var embeddedEtcd *embed.Etcd

const (
	testClientEndpoint string        = "127.0.0.1:12379"
	testPeerEndpoint   string        = "127.0.0.1:12380"
	embededEtcdDir     string        = "etcdhcp_test.etcd"
	testTtl            time.Duration = 42 * time.Minute
)

func testSetup(etcdKvs *map[string]string) error {
	if err := startEmbEtcd(); err != nil {
		return err
	}
	if err := createKvs(etcdKvs); err != nil {
		return err
	}
	return nil
}

func testTearDown() error {
	if err := deleteKvs(); err != nil {
		return err
	}
	stopEmbEtcd()
	return nil
}

func startEmbEtcd() error {
	if embeddedEtcd != nil {
		return errors.New("Embedded etcd server is already running or has not been stopped successfully")
	}
	cfg := embed.NewConfig()
	cfg.Dir = embededEtcdDir
	testClientUrl, _ := url.Parse("http://" + testClientEndpoint)
	testPeerUrl, _ := url.Parse("http://" + testPeerEndpoint)
	cfg.ACUrls = []url.URL{*testClientUrl}
	cfg.LCUrls = []url.URL{*testClientUrl}
	cfg.APUrls = []url.URL{*testPeerUrl}
	cfg.LPUrls = []url.URL{*testPeerUrl}
	cfg.InitialCluster = cfg.Name + "=" + testPeerUrl.String()
	cfg.EnableV2 = false
	e, err := embed.StartEtcd(cfg)
	if err != nil {
		return errors.Wrap(err, "Failed to start embedded etcd server for some reasons")
	}
	select {
	case <-e.Server.ReadyNotify():
		embeddedEtcd = e
		return nil
	case <-time.After(15 * time.Second):
		e.Close()
		return errors.New("Starting embedded etcd server timed out")
	case err := <-e.Err():
		e.Close()
		return err
	}
}

func stopEmbEtcd() {
	embeddedEtcd.Close()
	embeddedEtcd = nil
}

func createKvs(etcdKvs *map[string]string) error {
	c, err := etcd.New(etcd.Config{Endpoints: []string{testClientEndpoint}})
	if err != nil {
		return errors.Wrap(err, "Cannot setup etcd client used for creating test data")
	}
	defer c.Close()

	kvc := etcd.NewKV(c)
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	for k, v := range *etcdKvs {
		_, err := kvc.Put(ctx, k, v)
		if err != nil {
			return errors.Wrap(err, "Error occured during creating a key-value")
		}
	}
	return nil
}

func deleteKvs() error {
	c, err := etcd.New(etcd.Config{Endpoints: []string{testClientEndpoint}})
	if err != nil {
		return errors.Wrap(err, "Cannot setup etcd client used for creating test data")
	}
	defer c.Close()

	kvc := etcd.NewKV(c)
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	_, err = kvc.Delete(ctx, "", etcd.WithPrefix())
	if err != nil {
		return errors.Wrap(err, "Error occured during deleting all key-values")
	}
	return nil
}

type testingWriter struct {
	ExpectedRcode             int
	ExpectedAuthoritative     bool
	ExpectedAnswerSection     []*regexp.Regexp
	ExpectedAdditionalSection []*regexp.Regexp
	ExpectedAuthoritySection  []*regexp.Regexp
	WriteMsgIsCalled          *bool
	T                         *testing.T
}

func (w testingWriter) LocalAddr() net.Addr {
	// w.T.Error("LocalAddr() is not expected to be called")
	return nil
}

func (w testingWriter) RemoteAddr() net.Addr {
	// w.T.Error("RemoteAddr() is not expected to be called")
	return nil
}

func (w testingWriter) WriteMsg(msg *dns.Msg) error {
	if msg.MsgHdr.Authoritative != w.ExpectedAuthoritative {
		if w.ExpectedAuthoritative {
			w.T.Error("Answers are expected to be `authoritative`, but it's not")
		} else {
			w.T.Error("Answers are expected not to be `authoritative`, but it is")
		}
	}
	if msg.MsgHdr.Rcode != w.ExpectedRcode {
		actualRcodeStr := dns.RcodeToString[msg.MsgHdr.Rcode]
		expectedRcodeStr := dns.RcodeToString[w.ExpectedRcode]
		w.T.Errorf("Rcode indicates %s (Expected: %s)", actualRcodeStr, expectedRcodeStr)
	}

	answerRecords := []string{}
	for _, rr := range msg.Answer {
		answerRecords = append(answerRecords, rr.String())
	}
	if !strSliceUnorderedRegexpMatch(answerRecords, w.ExpectedAnswerSection) {
		w.T.Errorf("Unexpected RRs in answer section\nExpected pattern: %v\nActual: %v", w.ExpectedAnswerSection, answerRecords)
	}

	extraRecords := []string{}
	for _, rr := range msg.Extra {
		extraRecords = append(extraRecords, rr.String())
	}
	if !strSliceUnorderedRegexpMatch(extraRecords, w.ExpectedAdditionalSection) {
		w.T.Errorf("Unexpected RRs in additional section\nExpected pattern: %v\nActual: %v", w.ExpectedAdditionalSection, extraRecords)
	}

	nsRecords := []string{}
	for _, rr := range msg.Ns {
		nsRecords = append(nsRecords, rr.String())
	}
	if !strSliceUnorderedRegexpMatch(nsRecords, w.ExpectedAuthoritySection) {
		w.T.Errorf("Unexpected RRs in authority section\nExpected pattern: %v\nActual: %v", w.ExpectedAuthoritySection, nsRecords)
	}

	*w.WriteMsgIsCalled = true
	return nil
}

func (w testingWriter) Write([]byte) (int, error) {
	w.T.Error("Write([]byte) is not expected to be called")
	return 0, nil
}

func (w testingWriter) Close() error {
	w.T.Error("Close() is not expected to be called")
	return nil
}

func (w testingWriter) TsigStatus() error {
	w.T.Error("TsigStatus() is not expected to be called")
	return nil
}

func (w testingWriter) TsigTimersOnly(bool) {
	w.T.Error("TsigTimersOnly(bool) is not expected to be called")
}

func (w testingWriter) Hijack() {
	w.T.Error("Hijack() is not expected to be called")
}

func strSliceUnorderedRegexpMatch(str []string, pat []*regexp.Regexp) bool {
	if len(str) != len(pat) {
		return false
	}
	matchCounter := map[int]struct{}{}
	for _, s := range str {
		for i, p := range pat {
			_, ok := matchCounter[i]
			if ok {
				continue
			}
			if p.MatchString(s) {
				matchCounter[i] = struct{}{}
				break
			}
		}
	}
	return len(matchCounter) == len(str)
}

func TestMain(m *testing.M) {
	etcdKvs := map[string]string{
		"etcdhcp::test-zone::coredns::ns::ns1":                                         "192.168.1.254",
		"etcdhcp::test-zone::coredns::ns::ns2":                                         "192.168.2.254",
		"etcdhcp::test-zone::coredns::ip::static1":                                     "192.168.1.11",
		"etcdhcp::test-zone::coredns::ip::static2":                                     "192.168.1.12",
		"etcdhcp::test-zone::coredns::nic::bound1::00:00:00:00:00:01":                  "00:00:00:00:00:01",
		"etcdhcp::test-zone::coredns::nic::bound2::00:00:00:00:00:01":                  "00:00:00:00:00:01", // another name for the NIC
		"etcdhcp::test-zone::coredns::nic::bound3::00:00:00:00:00:02":                  "00:00:00:00:00:02",
		"etcdhcp::test-zone::hostnames::nic::dhcp-option-hostname1::00:00:00:00:00:02": "00:00:00:00:00:02",
		"etcdhcp::test-zone::hostnames::nic::dhcp-option-hostname2::00:00:00:00:00:03": "00:00:00:00:00:03",
		"etcdhcp::test-zone::hostnames::nic::dhcp-option-hostname2::00:00:00:00:00:04": "00:00:00:00:00:04", // the same name with other DHCP client
		"etcdhcp::test-zone::hostnames::nic::bound1::00:00:00:00:00:05":                "00:00:00:00:00:05", // the same name with bound name
		"etcdhcp::test-zone::hostnames::nic::ns2::00:00:00:00:00:06":                   "00:00:00:00:00:06", // the same name with name server
		"etcdhcp::test-zone::nics::leased::00:00:00:00:00:01":                          "192.168.1.101",
		"etcdhcp::test-zone::nics::leased::00:00:00:00:00:02":                          "192.168.1.102",
		"etcdhcp::test-zone::nics::leased::00:00:00:00:00:03":                          "192.168.1.103",
		"etcdhcp::test-zone::nics::leased::00:00:00:00:00:04":                          "192.168.1.104",
		"etcdhcp::test-zone::nics::leased::00:00:00:00:00:05":                          "192.168.1.105",
		"etcdhcp::test-zone::nics::leased::00:00:00:00:00:06":                          "192.168.1.106",
	}

	if err := testSetup(&etcdKvs); err != nil {
		log.Println(err)
		log.Printf("Failed to setup test environment. You might have to remove %v directory.\n", embededEtcdDir)
		os.Exit(1)
	}
	status := m.Run()
	if err := testTearDown(); err != nil {
		log.Println(err)
		log.Printf("Failed to teardown test environment. You might have to remove %v directory.\n", embededEtcdDir)
		os.Exit(1)
	}
	os.Exit(status)
}

func TestQueryNSNoramlly(t *testing.T) {
	e := Etcdhcp{
		Zones:      []string{"test-zone.local."},
		Endpoint:   []string{testClientEndpoint},
		EtcdPrefix: "etcdhcp::test-zone::",
		TTL:        testTtl,
	}
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	calledFlag := false
	w := testingWriter{
		ExpectedRcode:         dns.RcodeSuccess,
		ExpectedAuthoritative: true,
		ExpectedAnswerSection: []*regexp.Regexp{
			regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("%s\t%d\t%s\t%s\t%s", "test-zone.local.", testTtl/time.Second, "IN", "NS", "ns1.test-zone.local."))),
			regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("%s\t%d\t%s\t%s\t%s", "test-zone.local.", testTtl/time.Second, "IN", "NS", "ns2.test-zone.local."))),
		},
		ExpectedAdditionalSection: []*regexp.Regexp{
			regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("%s\t%d\t%s\t%s\t%s", "ns1.test-zone.local.", testTtl/time.Second, "IN", "A", "192.168.1.254"))),
			regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("%s\t%d\t%s\t%s\t%s", "ns2.test-zone.local.", testTtl/time.Second, "IN", "A", "192.168.2.254"))),
		},
		ExpectedAuthoritySection: []*regexp.Regexp{},
		WriteMsgIsCalled:         &calledFlag,
		T:                        t,
	}
	msg := new(dns.Msg)
	msg.SetQuestion("test-zone.local.", dns.TypeNS)
	e.ServeDNS(ctx, w, msg)
	if !*w.WriteMsgIsCalled {
		t.Error("WriteMsg function has not been called")
	}
}

func TestQueryAOfNameserver(t *testing.T) {
	e := Etcdhcp{
		Zones:      []string{"test-zone.local."},
		Endpoint:   []string{testClientEndpoint},
		EtcdPrefix: "etcdhcp::test-zone::",
		TTL:        testTtl,
	}
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	calledFlag := false
	w := testingWriter{
		ExpectedRcode:         dns.RcodeSuccess,
		ExpectedAuthoritative: true,
		ExpectedAnswerSection: []*regexp.Regexp{
			regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("%s\t%d\t%s\t%s\t%s", "ns2.test-zone.local.", testTtl/time.Second, "IN", "A", "192.168.2.254"))),
		},
		ExpectedAdditionalSection: []*regexp.Regexp{},
		ExpectedAuthoritySection:  []*regexp.Regexp{},
		WriteMsgIsCalled:          &calledFlag,
		T:                         t,
	}
	msg := new(dns.Msg)
	msg.SetQuestion("ns2.test-zone.local.", dns.TypeA)
	e.ServeDNS(ctx, w, msg)
	if !*w.WriteMsgIsCalled {
		t.Error("WriteMsg function has not been called")
	}
}

func TestQueryAOfStaticHostname(t *testing.T) {
	e := Etcdhcp{
		Zones:      []string{"test-zone.local."},
		Endpoint:   []string{testClientEndpoint},
		EtcdPrefix: "etcdhcp::test-zone::",
		TTL:        testTtl,
	}
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	calledFlag := false
	w := testingWriter{
		ExpectedRcode:         dns.RcodeSuccess,
		ExpectedAuthoritative: true,
		ExpectedAnswerSection: []*regexp.Regexp{
			regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("%s\t%d\t%s\t%s\t%s", "static1.test-zone.local.", testTtl/time.Second, "IN", "A", "192.168.1.11"))),
		},
		ExpectedAdditionalSection: []*regexp.Regexp{},
		ExpectedAuthoritySection:  []*regexp.Regexp{},
		WriteMsgIsCalled:          &calledFlag,
		T:                         t,
	}
	msg := new(dns.Msg)
	msg.SetQuestion("static1.test-zone.local.", dns.TypeA)
	e.ServeDNS(ctx, w, msg)
	if !*w.WriteMsgIsCalled {
		t.Error("WriteMsg function has not been called")
	}
}

func TestQueryAOfBoundHostname(t *testing.T) {
	e := Etcdhcp{
		Zones:      []string{"test-zone.local."},
		Endpoint:   []string{testClientEndpoint},
		EtcdPrefix: "etcdhcp::test-zone::",
		TTL:        testTtl,
	}
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	calledFlag := false
	w := testingWriter{
		ExpectedRcode:         dns.RcodeSuccess,
		ExpectedAuthoritative: true,
		ExpectedAnswerSection: []*regexp.Regexp{
			regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("%s\t%d\t%s\t%s\t%s", "bound1.test-zone.local.", testTtl/time.Second, "IN", "A", "192.168.1.101"))),
		},
		ExpectedAdditionalSection: []*regexp.Regexp{},
		ExpectedAuthoritySection:  []*regexp.Regexp{},
		WriteMsgIsCalled:          &calledFlag,
		T:                         t,
	}
	msg := new(dns.Msg)
	msg.SetQuestion("bound1.test-zone.local.", dns.TypeA)
	e.ServeDNS(ctx, w, msg)
	if !*w.WriteMsgIsCalled {
		t.Error("WriteMsg function has not been called")
	}
}

func TestQueryAOfDhcpUniqueHostname(t *testing.T) {
	e := Etcdhcp{
		Zones:      []string{"test-zone.local."},
		Endpoint:   []string{testClientEndpoint},
		EtcdPrefix: "etcdhcp::test-zone::",
		TTL:        testTtl,
	}
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	calledFlag := false
	w := testingWriter{
		ExpectedRcode:         dns.RcodeSuccess,
		ExpectedAuthoritative: true,
		ExpectedAnswerSection: []*regexp.Regexp{
			regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("%s\t%d\t%s\t%s\t%s", "dhcp-option-hostname1.test-zone.local.", testTtl/time.Second, "IN", "A", "192.168.1.102"))),
		},
		ExpectedAdditionalSection: []*regexp.Regexp{},
		ExpectedAuthoritySection:  []*regexp.Regexp{},
		WriteMsgIsCalled:          &calledFlag,
		T:                         t,
	}
	msg := new(dns.Msg)
	msg.SetQuestion("dhcp-option-hostname1.test-zone.local.", dns.TypeA)
	e.ServeDNS(ctx, w, msg)
	if !*w.WriteMsgIsCalled {
		t.Error("WriteMsg function has not been called")
	}
}

func TestQueryAOfDhcpDuplicatedHostname(t *testing.T) {
	e := Etcdhcp{
		Zones:      []string{"test-zone.local."},
		Endpoint:   []string{testClientEndpoint},
		EtcdPrefix: "etcdhcp::test-zone::",
		TTL:        testTtl,
	}
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	calledFlag := false
	w := testingWriter{
		ExpectedRcode:         dns.RcodeSuccess,
		ExpectedAuthoritative: true,
		ExpectedAnswerSection: []*regexp.Regexp{
			regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("%s\t%d\t%s\t%s\t%s", "dhcp-option-hostname2.test-zone.local.", testTtl/time.Second, "IN", "A", "192.168.1.103"))),
			regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("%s\t%d\t%s\t%s\t%s", "dhcp-option-hostname2.test-zone.local.", testTtl/time.Second, "IN", "A", "192.168.1.104"))),
		},
		ExpectedAdditionalSection: []*regexp.Regexp{},
		ExpectedAuthoritySection:  []*regexp.Regexp{},
		WriteMsgIsCalled:          &calledFlag,
		T:                         t,
	}
	msg := new(dns.Msg)
	msg.SetQuestion("dhcp-option-hostname2.test-zone.local.", dns.TypeA)
	e.ServeDNS(ctx, w, msg)
	if !*w.WriteMsgIsCalled {
		t.Error("WriteMsg function has not been called")
	}
}

func TestQueryNonExistingNS(t *testing.T) {
	e := Etcdhcp{
		Zones:      []string{"test-zone.local."},
		Endpoint:   []string{testClientEndpoint},
		EtcdPrefix: "etcdhcp::test-zone::",
		TTL:        testTtl,
	}
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	calledFlag := false
	w := testingWriter{
		ExpectedRcode:             dns.RcodeNameError,
		ExpectedAuthoritative:     true,
		ExpectedAnswerSection:     []*regexp.Regexp{},
		ExpectedAdditionalSection: []*regexp.Regexp{},
		ExpectedAuthoritySection: []*regexp.Regexp{
			regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("%s\t%d\t%s\t%s\t%s", "test-zone.local.", testTtl/time.Second, "IN", "SOA", "ns1.test-zone.local. postmaster.test-zone.local.")) + ".*"),
		},
		WriteMsgIsCalled: &calledFlag,
		T:                t,
	}
	msg := new(dns.Msg)
	msg.SetQuestion("not-exist.test-zone.local.", dns.TypeNS)
	e.ServeDNS(ctx, w, msg)
	if !*w.WriteMsgIsCalled {
		t.Error("WriteMsg function has not been called")
	}
}

func TestQueryNonExistingA(t *testing.T) {
	e := Etcdhcp{
		Zones:      []string{"test-zone.local."},
		Endpoint:   []string{testClientEndpoint},
		EtcdPrefix: "etcdhcp::test-zone::",
		TTL:        testTtl,
	}
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	calledFlag := false
	w := testingWriter{
		ExpectedRcode:             dns.RcodeNameError,
		ExpectedAuthoritative:     true,
		ExpectedAnswerSection:     []*regexp.Regexp{},
		ExpectedAdditionalSection: []*regexp.Regexp{},
		ExpectedAuthoritySection: []*regexp.Regexp{
			regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("%s\t%d\t%s\t%s\t%s", "test-zone.local.", testTtl/time.Second, "IN", "SOA", "ns1.test-zone.local. postmaster.test-zone.local.")) + ".*"),
		},
		WriteMsgIsCalled: &calledFlag,
		T:                t,
	}
	msg := new(dns.Msg)
	msg.SetQuestion("not-exist.test-zone.local.", dns.TypeA)
	e.ServeDNS(ctx, w, msg)
	if !*w.WriteMsgIsCalled {
		t.Error("WriteMsg function has not been called")
	}
}

func TestQueryNoData(t *testing.T) {
	e := Etcdhcp{
		Zones:      []string{"test-zone.local."},
		Endpoint:   []string{testClientEndpoint},
		EtcdPrefix: "etcdhcp::test-zone::",
		TTL:        testTtl,
	}
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	calledFlag := false
	w := testingWriter{
		ExpectedRcode:             dns.RcodeSuccess,
		ExpectedAuthoritative:     true,
		ExpectedAnswerSection:     []*regexp.Regexp{},
		ExpectedAdditionalSection: []*regexp.Regexp{},
		ExpectedAuthoritySection: []*regexp.Regexp{
			regexp.MustCompile(regexp.QuoteMeta(fmt.Sprintf("%s\t%d\t%s\t%s\t%s", "test-zone.local.", testTtl/time.Second, "IN", "SOA", "ns1.test-zone.local. postmaster.test-zone.local.")) + ".*"),
		},
		WriteMsgIsCalled: &calledFlag,
		T:                t,
	}
	msg := new(dns.Msg)
	msg.SetQuestion("test-zone.local.", dns.TypeA)
	e.ServeDNS(ctx, w, msg)
	if !*w.WriteMsgIsCalled {
		t.Error("WriteMsg function has not been called")
	}
}
