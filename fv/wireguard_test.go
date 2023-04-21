// Copyright (c) 2020 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build fvtests
// +build fvtests

package fv_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/libcalico-go/lib/numorstring"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/projectcalico/felix/fv/connectivity"
	"github.com/projectcalico/felix/fv/infrastructure"
	"github.com/projectcalico/felix/fv/tcpdump"
	"github.com/projectcalico/felix/fv/utils"
	"github.com/projectcalico/felix/fv/workload"
	"github.com/projectcalico/libcalico-go/lib/apiconfig"
	api "github.com/projectcalico/libcalico-go/lib/apis/v3"
	"github.com/projectcalico/libcalico-go/lib/clientv3"
	"github.com/projectcalico/libcalico-go/lib/ipam"
	"github.com/projectcalico/libcalico-go/lib/net"
	"github.com/projectcalico/libcalico-go/lib/options"
)

const (
	wireguardInterfaceNameDefault       = "wireguard.cali"
	wireguardMTUDefault                 = 1420
	wireguardRoutingRulePriorityDefault = "99"
	wireguardListeningPortDefault       = 51820

	fakeWireguardPubKey = "jlkVyQYooZYzI2wFfNhSZez5eWh44yfq1wKVjLvSXgY="
)

var _ = infrastructure.DatastoreDescribe("_BPF-SAFE_ WireGuard-Supported", []apiconfig.DatastoreType{apiconfig.EtcdV3, apiconfig.Kubernetes}, func(getInfra infrastructure.InfraFactory) {
	const nodeCount = 2

	var (
		infra        infrastructure.DatastoreInfra
		felixes      []*infrastructure.Felix
		client       clientv3.Interface
		tcpdumps     []*tcpdump.TCPDump
		wls          [nodeCount]*workload.Workload // simulated host workloads
		cc           *connectivity.Checker
		routeEntries [nodeCount]string
	)

	BeforeEach(func() {
		// Run these tests only when the Host has Wireguard kernel module installed.
		if os.Getenv("FELIX_FV_WIREGUARD_AVAILABLE") != "true" {
			Skip("Skipping Wireguard supported tests.")
		}

		infra = getInfra()
		felixes, client = infrastructure.StartNNodeTopology(nodeCount, wireguardTopologyOptions(), infra)

		// To allow all ingress and egress, in absence of any Policy.
		infra.AddDefaultAllow()

		for i := range wls {
			wls[i] = createWorkloadWithAssignedIP(&infra, &client, fmt.Sprintf("10.65.%d.2", i), fmt.Sprintf("wl%d", i), felixes[i])

			// Prepare route entry.
			routeEntries[i] = fmt.Sprintf("10.65.%d.0/26 dev %s scope link", i, wireguardInterfaceNameDefault)

			felixes[i].TriggerDelayedStart()
		}
		// Swap route entry to match between workloads.
		routeEntries[0], routeEntries[1] = routeEntries[1], routeEntries[0]

		cc = &connectivity.Checker{}

		// Reset the set of tcp dumps between runs.
		tcpdumps = nil
	})

	AfterEach(func() {
		if CurrentGinkgoTestDescription().Failed {
			for _, felix := range felixes {
				felix.Exec("ip", "addr")
				felix.Exec("ip", "rule", "list")
				felix.Exec("ip", "route", "show", "table", "all")
				felix.Exec("ip", "route", "show", "cached")
				felix.Exec("wg")
			}
		}

		for _, wl := range wls {
			wl.Stop()
		}
		for _, tcpdump := range tcpdumps {
			tcpdump.Stop()
		}
		for _, felix := range felixes {
			felix.Stop()
		}

		if CurrentGinkgoTestDescription().Failed {
			infra.DumpErrorData()
		}
		infra.Stop()
	})

	Context("with Wireguard enabled", func() {
		BeforeEach(func() {
			for i, felix := range felixes {
				// Check the Wireguard device exists.
				Eventually(func() error {
					out, err := felix.ExecOutput("ip", "link", "show", wireguardInterfaceNameDefault)
					if err != nil {
						return err
					}
					if strings.Contains(out, wireguardInterfaceNameDefault) {
						return nil
					}
					return fmt.Errorf("felix %d has no Wireguard device", i)
				}, "10s", "100ms").ShouldNot(HaveOccurred())
			}
		})

		It("the Wireguard routing rule should exist", func() {
			for _, felix := range felixes {
				Eventually(func() string {
					return getWireguardRoutingRule(felix)
				}, "5s", "100ms").Should(MatchRegexp("\\d+:\\s+from all fwmark 0/0x\\d+ lookup \\d+"))
			}
		})

		It("the Wireguard route-table entry should exist", func() {
			for i, felix := range felixes {
				Eventually(func() string {
					return getWireguardRouteEntry(felix)
				}, "5s", "100ms").Should(ContainSubstring(routeEntries[i]))
			}
		})

		It("the Wireguard device should be configurable", func() {
			disableWireguard(client)

			// Old configuration should disappear.
			for _, felix := range felixes {
				Eventually(func() string {
					out, _ := felix.ExecOutput("ip", "-d", "link", "show", wireguardInterfaceNameDefault)
					return out
				}, "10s", "100ms").Should(BeEmpty())
				Eventually(func() string {
					out, err := felix.ExecOutput("ip", "rule", "show", "pref", wireguardRoutingRulePriorityDefault)
					Expect(err).NotTo(HaveOccurred())
					return out
				}, "10s", "100ms").Should(BeEmpty())
			}

			// Change Wireguard configuration.
			ifaceName := "wg0"
			mtu := 1400
			rule := 100
			port := 28150
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			fc, err := client.FelixConfigurations().Get(ctx, "default", options.GetOptions{})
			Expect(err).NotTo(HaveOccurred())
			fc.Spec.WireguardInterfaceName = ifaceName
			fc.Spec.WireguardMTU = &mtu
			fc.Spec.WireguardListeningPort = &port
			fc.Spec.WireguardRoutingRulePriority = &rule
			_, err = client.FelixConfigurations().Update(ctx, fc, options.SetOptions{})
			Expect(err).NotTo(HaveOccurred())

			enableWireguard(client)

			// New Wireguard device should appear with default MTU, etc.
			for _, felix := range felixes {
				Eventually(func() string {
					out, _ := felix.ExecOutput("ip", "-d", "link", "show", ifaceName)
					return out
				}, "10s", "100ms").Should(ContainSubstring(fmt.Sprintf("mtu %d", mtu)))
			}

			// Expect the settings to be changed on the device.
			for _, felix := range felixes {
				Eventually(func() string {
					out, err := felix.ExecOutput("wg")
					Expect(err).NotTo(HaveOccurred())
					return out
				}, "10s", "100ms").Should(ContainSubstring(fmt.Sprintf("listening port: %d", port)))
				Eventually(func() string {
					out, err := felix.ExecOutput("ip", "rule", "show", "pref", fmt.Sprintf("%d", rule))
					Expect(err).NotTo(HaveOccurred())
					return out
				}, "10s", "100ms").ShouldNot(BeEmpty())
			}
		})

		It("v3 node resource annotations should contain public-keys", func() {
			for _, felix := range felixes {
				Eventually(func() string {
					node, err := client.Nodes().Get(context.Background(), felix.Hostname, options.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					return node.Status.WireguardPublicKey
				}, "5s", "100ms").ShouldNot(BeEmpty())
			}
		})

		It("v3 node resource annotations should automatically heal", func() {
			for _, felix := range felixes {
				// Get the original public-key.
				node, err := client.Nodes().Get(context.Background(), felix.Hostname, options.GetOptions{})
				Expect(err).NotTo(HaveOccurred())
				wgPubKeyOrig := node.Status.WireguardPublicKey

				// overwrite public-key by fake but valid Wireguard key.
				node.Status.WireguardPublicKey = fakeWireguardPubKey
				_, err = client.Nodes().Update(context.Background(), node, options.SetOptions{})
				Expect(err).NotTo(HaveOccurred())

				Eventually(func() string {
					node, err := client.Nodes().Get(context.Background(), felix.Hostname, options.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					return node.Status.WireguardPublicKey
				}, "5s", "100ms").Should(Equal(wgPubKeyOrig))
			}
		})
	})

	Context("traffic with Wireguard enabled", func() {
		// Checks the TCP dump for a count value. Retries until count is correct, or fails after 1.5s.
		waitForPackets := func(t *tcpdump.TCPDump, timeout time.Time, name string, num int) error {
			for ; ; time.Now().Before(timeout) {
				if num == 0 && t.MatchCount(name) > 0 {
					// We expect no traffic, but got some.  Error immediately.
					break
				}
				if t.MatchCount(name) >= num {
					// We expected some packets and have got at least the required number (we allow more for handshake
					// etc.)
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			return fmt.Errorf("incorrect packet count for %s; expected=%d actual=%d", name, num, t.MatchCount(name))
		}

		// Runs wg and extracts the received and sent packet counts.
		getWgStatistics := func(felix *infrastructure.Felix) (sent, rcvd string) {
			xferRegExp := regexp.MustCompile(`transfer:\s+([0-9a-zA-Z. ]+)\s+received,\s+([0-9a-zA-Z. ]+)\s+sent`)
			out, err := felix.ExecOutput("wg")
			Expect(err).NotTo(HaveOccurred())
			matches := xferRegExp.FindStringSubmatch(out)
			if len(matches) != 3 {
				return
			}
			return matches[2], matches[1]
		}

		// Checks connectivity between workloads 0 and 1, checking that eth0 traffic is via wireguard port and that
		// there are non-empty wireguard stats.
		checkConn := func() error {
			// Reset TCP packet counts.
			By("Resetting the TCP dump counts")
			for i := range felixes {
				tcpdumps[i].ResetCount("numInTunnelPackets")
				tcpdumps[i].ResetCount("numOutTunnelPackets")
				tcpdumps[i].ResetCount("numWorkload0to1Packets")
				tcpdumps[i].ResetCount("numWorkload1to0Packets")
			}

			// Send packets to and from workloads on each felix.
			By("Sending packets W1->W2 and W2->W1")
			if err, _ := wls[0].SendPacketsTo(wls[1].IP, 5, 56); err != nil {
				return err
			}
			if err, _ := wls[1].SendPacketsTo(wls[0].IP, 5, 56); err != nil {
				return err
			}

			// Now check the packet counts are as expected. We should have no WL->WL traffic visible on eth0, but
			// we should be able to see tunnel traffic. Since we want to verify
			By("Checking the packet stats from tcpdump")
			timeout := time.Now().Add(2 * time.Second)
			for i := range felixes {
				if err := waitForPackets(tcpdumps[i], timeout, "numInTunnelPackets", 10); err != nil {
					return err
				} else if err := waitForPackets(tcpdumps[i], timeout, "numOutTunnelPackets", 10); err != nil {
					return err
				} else if err := waitForPackets(tcpdumps[i], timeout, "numWorkload0to1Packets", 0); err != nil {
					return err
				} else if err := waitForPackets(tcpdumps[i], timeout, "numWorkload1to0Packets", 0); err != nil {
					return err
				}
			}

			By("Checking the packet stats from wg")
			for i := range felixes {
				rcvd, sent := getWgStatistics(felixes[i])
				//TODO: counter compare sent/rcvd data from wg tunnel on each node.
				Expect(rcvd).NotTo(BeEmpty())
				Expect(sent).NotTo(BeEmpty())
			}
			return nil
		}

		BeforeEach(func() {
			// Tunnel readiness checks.
			for i, felix := range felixes {
				// Check the Wireguard device exists.
				Eventually(func() error {
					out, err := felix.ExecOutput("ip", "link", "show", wireguardInterfaceNameDefault)
					if err != nil {
						return err
					}
					if strings.Contains(out, wireguardInterfaceNameDefault) {
						return nil
					}
					return fmt.Errorf("felix %d has no Wireguard device", i)
				}, "10s", "100ms").ShouldNot(HaveOccurred())
			}

			for _, felix := range felixes {
				// Check the rule exists.
				Eventually(func() string {
					return getWireguardRoutingRule(felix)
				}, "10s", "100ms").Should(MatchRegexp("\\d+:\\s+from all fwmark 0/0x\\d+ lookup \\d+"))
			}

			for i, felix := range felixes {
				// Check the route entry exists.
				Eventually(func() string {
					return getWireguardRouteEntry(felix)
				}, "10s", "100ms").Should(ContainSubstring(routeEntries[i]))
			}

			tcpdumps = make([]*tcpdump.TCPDump, nodeCount)
			for i, felix := range felixes {
				// felix tcpdump
				tcpdump := felix.AttachTCPDump("eth0")

				inTunnelPacketsPattern := fmt.Sprintf("IP %s\\.51820 > \\d+\\.\\d+\\.\\d+\\.\\d+\\.51820: UDP", felix.IP)
				tcpdump.AddMatcher("numInTunnelPackets", regexp.MustCompile(inTunnelPacketsPattern))
				outTunnelPacketsPattern := fmt.Sprintf("IP \\d+\\.\\d+\\.\\d+\\.\\d+\\.51820 > %s\\.51820: UDP", felix.IP)
				tcpdump.AddMatcher("numOutTunnelPackets", regexp.MustCompile(outTunnelPacketsPattern))
				workload01PacketsPattern := fmt.Sprintf("IP %s\\.\\d+ > %s\\.\\d+: ", wls[0].IP, wls[1].IP)
				tcpdump.AddMatcher("numWorkload0to1Packets", regexp.MustCompile(workload01PacketsPattern))
				workload10PacketsPattern := fmt.Sprintf("IP %s\\.\\d+ > %s\\.\\d+: ", wls[1].IP, wls[0].IP)
				tcpdump.AddMatcher("numWorkload1to0Packets", regexp.MustCompile(workload10PacketsPattern))

				tcpdump.Start()
				tcpdumps[i] = tcpdump
			}
		})

		It("between pod to pod should be allowed and encrypted using wg tunnel", func() {
			Eventually(checkConn, "10s", "100ms").ShouldNot(HaveOccurred())
		})

		for _, ai := range []bool{true, false} {
			allInterfaces := ai
			desc := "should add wireguard port as a failsafe"
			if ai {
				desc += " (using * HostEndpoint)"
			} else {
				desc += " (using eth0 HostEndpoint)"
			}
			It(desc, func() {
				By("Creating policy to deny wireguard port on main felix host endpoint")
				policy := api.NewGlobalNetworkPolicy()
				policy.Name = "deny-wg-port"
				prot := numorstring.ProtocolFromString(numorstring.ProtocolUDP)
				policy.Spec.Egress = []api.Rule{
					{
						// Deny egress UDP to the wireguard port.
						Action:   api.Deny,
						Protocol: &prot,
						Destination: api.EntityRule{
							Selector: "has(host-endpoint)",
							Ports:    []numorstring.Port{numorstring.SinglePort(wireguardListeningPortDefault)},
						},
					},
					{Action: api.Allow},
				}
				policy.Spec.Ingress = []api.Rule{
					{
						// Deny all UDP traffic to the hosts.
						Action:   api.Deny,
						Protocol: &prot,
						Destination: api.EntityRule{
							Selector: "has(host-endpoint)",
							Ports:    []numorstring.Port{numorstring.SinglePort(wireguardListeningPortDefault)},
						},
					},
					{Action: api.Allow},
				}
				policy.Spec.Selector = "all()"
				policy.Spec.Types = []api.PolicyType{api.PolicyTypeIngress, api.PolicyTypeEgress}
				_, err := client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
				Expect(err).NotTo(HaveOccurred())

				By("Creating a HostEndpoint for each Felix")
				for _, f := range felixes {
					hep := api.NewHostEndpoint()
					hep.Name = "hep-" + f.Name
					hep.Labels = map[string]string{
						"name":          hep.Name,
						"hostname":      f.Hostname,
						"host-endpoint": "true",
					}
					hep.Spec.Node = f.Hostname
					hep.Spec.ExpectedIPs = []string{f.IP}
					if allInterfaces {
						hep.Spec.InterfaceName = "*"
					} else {
						hep.Spec.InterfaceName = "eth0"
					}
					_, err := client.HostEndpoints().Create(utils.Ctx, hep, options.SetOptions{})
					Expect(err).NotTo(HaveOccurred())
				}

				By("Checking there is eventually and consistently connectivity between the workloads using wg")
				Eventually(checkConn, "5s", "100ms").ShouldNot(HaveOccurred())
				Consistently(checkConn, "2s", "100ms").ShouldNot(HaveOccurred())
			})
		}

		readPolicy := func(name string, action api.Action) error {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			policy, err := client.GlobalNetworkPolicies().Get(ctx, name, options.GetOptions{})
			if err == nil {
				if len(policy.Spec.Egress) > 0 &&
					policy.Spec.Egress[0].Action == action {
					return nil
				}
			}
			return fmt.Errorf("policy not applied")
		}

		It("between pod to pod should be encrypted using wg tunnel with egress policies applied", func() {
			policy := api.NewGlobalNetworkPolicy()

			policy.Name = "f01-egress-deny"
			order := float64(20)
			policy.Spec.Order = &order
			policy.Spec.Egress = []api.Rule{{Action: api.Deny}}
			policy.Spec.Selector = fmt.Sprintf("name in { '%s', '%s'}", wls[0].Name, wls[1].Name)
			_, err := client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(5 * time.Second)
			Eventually(func() error {
				return readPolicy(policy.Name, api.Deny)
			}, "5s", "100ms").ShouldNot(HaveOccurred())

			cc.ExpectNone(wls[0], wls[1])
			cc.ExpectNone(wls[1], wls[0])
			cc.CheckConnectivity()

			By("verifying tunnelled packet count is zero")
			for i := range felixes {
				Consistently(func() int {
					return tcpdumps[i].MatchCount("numInTunnelPackets")
				}, "5s", "100ms").Should(BeNumerically("==", 0))
				Consistently(func() int {
					return tcpdumps[i].MatchCount("numOutTunnelPackets")
				}, "5s", "100ms").Should(BeNumerically("==", 0))
			}

			cc.ResetExpectations()

			policy = api.NewGlobalNetworkPolicy()
			policy.Name = "f01-egress-allow"
			order = float64(10)
			policy.Spec.Order = &order // prioritized over deny policy above.
			policy.Spec.Egress = []api.Rule{{Action: api.Allow}}
			policy.Spec.Selector = fmt.Sprintf("name in { '%s', '%s'}", wls[0].Name, wls[1].Name)
			_, err = client.GlobalNetworkPolicies().Create(utils.Ctx, policy, utils.NoOptions)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() error {
				return readPolicy(policy.Name, api.Allow)
			}, "5s", "100ms").ShouldNot(HaveOccurred())

			cc.OnFail = func(msg string) {
				log.WithField("msg", msg).Info("Connectivity Failed after 30seconds")
				log.Info("Checking connectivity using ping between wls0 and wls1")

				err, errstr := wls[0].SendPacketsTo(wls[1].IP, 5, 100)
				log.Infof("wls0 to wls1: err=%v, errstr=%s", err, errstr)
				err, errstr = wls[1].SendPacketsTo(wls[0].IP, 5, 100)
				log.Infof("wls1 to wls0: err=%v, errstr=%s", err, errstr)
			}
			cc.ExpectSome(wls[0], wls[1])
			cc.ExpectSome(wls[1], wls[0])
			cc.CheckConnectivityWithTimeout(30 * time.Second)

			By("verifying tunnelled packet count is non-zero")
			for i := range felixes {
				Eventually(func() int {
					return tcpdumps[i].MatchCount("numInTunnelPackets")
				}, "10s", "100ms").Should(BeNumerically(">", 0))
				Eventually(func() int {
					return tcpdumps[i].MatchCount("numOutTunnelPackets")
				}, "10s", "100ms").Should(BeNumerically(">", 0))
			}
		})
	})

	Context("with Wireguard disabled", func() {
		BeforeEach(func() {
			disableWireguard(client)

			// Check Wireguard device doesn't exist.
			for _, felix := range felixes {
				Eventually(func() string {
					out, _ := felix.ExecOutput("ip", "link", "show", wireguardInterfaceNameDefault)
					return out
				}, "10s", "100ms").Should(BeEmpty())
			}

			// Check that Wireguard routing rule doesn't exist.
			for _, felix := range felixes {
				Eventually(func() string {
					return getWireguardRoutingRule(felix)
				}, "10s", "100ms").Should(BeEmpty())
			}

			// Check Wireguard route table entry doesn't exist.
			for i, felix := range felixes {
				Eventually(func() string {
					return getWireguardRouteEntry(felix)
				}, "10s", "100ms").ShouldNot(ContainSubstring(routeEntries[i]))
			}
		})

		It("v3 node resource shouldn't contain public-key", func() {
			for _, felix := range felixes {
				Eventually(func() string {
					node, err := client.Nodes().Get(context.Background(), felix.Hostname, options.GetOptions{})
					Expect(err).NotTo(HaveOccurred())
					return node.Status.WireguardPublicKey
				}, "10s", "100ms").Should(BeEmpty())
			}
		})

		It("workload connectivity remains but uses un-encrypted tunnel", func() {
			cc.ExpectSome(wls[0], wls[1])
			cc.ExpectSome(wls[1], wls[0])
			cc.CheckConnectivity()

			for _, felix := range felixes {
				Eventually(func() string {
					// No tunnel implies un-encrypted communication.
					out, err := felix.ExecOutput("wg")
					Expect(err).NotTo(HaveOccurred())
					return out
				}, "10s", "100ms").Should(BeEmpty())
			}
		})
	})
})

var _ = infrastructure.DatastoreDescribe("WireGuard-Unsupported", []apiconfig.DatastoreType{apiconfig.EtcdV3, apiconfig.Kubernetes}, func(getInfra infrastructure.InfraFactory) {
	var (
		infra   infrastructure.DatastoreInfra
		felixes []*infrastructure.Felix
	)

	BeforeEach(func() {
		// Run these tests only when the Host does not have Wireguard available.
		if os.Getenv("FELIX_FV_WIREGUARD_AVAILABLE") == "true" {
			Skip("Skipping Wireguard unsupported tests.")
		}

		// Setup a single node cluster.
		const nodeCount = 1

		infra = getInfra()
		felixes, _ = infrastructure.StartNNodeTopology(nodeCount, wireguardTopologyOptions(), infra)

		// Install a default profile that allows all ingress and egress, in the absence of any Policy.
		infra.AddDefaultAllow()

		felixes[0].TriggerDelayedStart()
	})

	AfterEach(func() {
		if CurrentGinkgoTestDescription().Failed {
			felixes[0].Exec("ip", "link")
			felixes[0].Exec("wg")
		}

		felixes[0].Stop()

		if CurrentGinkgoTestDescription().Failed {
			infra.DumpErrorData()
		}
		infra.Stop()
	})

	It("no Wireguard device exists", func() {
		Eventually(func() string {
			out, err := felixes[0].ExecOutput("ip", "link", "show", wireguardInterfaceNameDefault)
			Expect(err).To(HaveOccurred())
			return out
		}, "10s", "100ms").Should(BeEmpty())
	})

	It("no wg tool info exists", func() {
		Eventually(func() string {
			out, err := felixes[0].ExecOutput("wg")
			Expect(err).NotTo(HaveOccurred())
			return out
		}, "10s", "100ms").Should(BeEmpty())
	})
})

var _ = infrastructure.DatastoreDescribe("_BPF-SAFE_ WireGuard-Supported 3 node cluster", []apiconfig.DatastoreType{apiconfig.EtcdV3, apiconfig.Kubernetes}, func(getInfra infrastructure.InfraFactory) {
	const nodeCount = 3

	var (
		infra   infrastructure.DatastoreInfra
		felixes []*infrastructure.Felix
		client  clientv3.Interface

		wls      [nodeCount]*workload.Workload // simulated host workloads
		cc       *connectivity.Checker
		tcpdumps []*tcpdump.TCPDump
	)

	BeforeEach(func() {
		// Run these tests only when the Host has Wireguard kernel module available.
		if os.Getenv("FELIX_FV_WIREGUARD_AVAILABLE") != "true" {
			Skip("Skipping Wireguard supported tests.")
		}

		infra = getInfra()
		felixes, client = infrastructure.StartNNodeTopology(nodeCount, wireguardTopologyOptions(), infra)

		// To allow all ingress and egress, in absence of any Policy.
		infra.AddDefaultAllow()

		for i := range wls {
			wls[i] = createWorkloadWithAssignedIP(&infra, &client, fmt.Sprintf("10.65.%d.2", i), fmt.Sprintf("wl%d", i), felixes[i])
		}

		// Create 'borrowed' workloads e.g. create workload on felix-0 with IP
		// borrowed from IPAM block from felix-1.
		_ = createWorkloadWithAssignedIP(&infra, &client, "10.65.0.4", "borrowed-0", felixes[1])
		_ = createWorkloadWithAssignedIP(&infra, &client, "10.65.1.4", "borrowed-1", felixes[0])

		for i := range felixes {
			felixes[i].TriggerDelayedStart()
		}

		cc = &connectivity.Checker{}
	})

	AfterEach(func() {
		if CurrentGinkgoTestDescription().Failed {
			for _, felix := range felixes {
				felix.Exec("ip", "addr")
				felix.Exec("ip", "rule", "list")
				felix.Exec("ip", "route", "show", "table", "all")
				felix.Exec("ip", "route", "show", "cached")
				felix.Exec("wg")
			}
		}

		for _, wl := range wls {
			wl.Stop()
		}

		for _, tcpdump := range tcpdumps {
			tcpdump.Stop()
		}

		for _, felix := range felixes {
			felix.Stop()
		}

		if CurrentGinkgoTestDescription().Failed {
			infra.DumpErrorData()
		}
		infra.Stop()
	})

	It("Workload with borrowed IP should be 'handled' on felix 0 and 1", func() {
		// Check that felix-0, felix-1 is ready
		// 1. by checking, Wireguard interface exist.
		Eventually(func() error {
			for i := range []int{0, 1} {
				out, err := felixes[i].ExecOutput("ip", "link")
				if err != nil {
					return err
				}
				if strings.Contains(out, wireguardInterfaceNameDefault) {
					continue
				}
				return fmt.Errorf("felix-%d has no wireguard device", i)
			}
			return nil
		}, "10s", "100ms").ShouldNot(HaveOccurred())
		// 2. by checking, Wireguard rule exist.
		for i := range []int{0, 1} {
			Eventually(func() string {
				return getWireguardRoutingRule(felixes[i])
			}, "10s", "100ms").Should(MatchRegexp(fmt.Sprintf("\\d+:\\s+from all fwmark 0/0x\\d+ lookup \\d+")))
		}
		// 3. by checking, Wireguard route table exist.
		for i := range []int{0, 1} {
			Eventually(func() string {
				return getWireguardRouteEntry(felixes[i])
			}, "10s", "100ms").Should(ContainSubstring("dev wireguard.cali scope link"))
		}

		By("verifying WireGuard route table should show 'throw' entry on felix 0 and 1")
		Eventually(func() string {
			return getWireguardRouteEntry(felixes[0])
		}, "10s", "100ms").Should(ContainSubstring("throw 10.65.1.4"))
		Eventually(func() string {
			return getWireguardRouteEntry(felixes[1])
		}, "10s", "100ms").Should(ContainSubstring("throw 10.65.0.4"))
	})

	Context("with Wireguard disabled in node-3", func() {
		BeforeEach(func() {
			// Disable WireGuard on felix-2.
			disableWireguardForFelix(client, "node."+felixes[2].Hostname)

			// Check felix-2 is ready with WireGuard disabled.
			Eventually(func() string {
				out, _ := felixes[2].ExecOutput("ip", "link", "show", wireguardInterfaceNameDefault)
				return out
			}, "10s", "100ms").Should(BeEmpty())

			Eventually(func() string {
				return getWireguardRoutingRule(felixes[2])
			}, "10s", "100ms").Should(BeEmpty())
			Eventually(func() string {
				return getWireguardRouteEntry(felixes[2])
			}, "10s", "100ms").ShouldNot(ContainSubstring("dev wireguard.cali scope link"))

			// Check felix-0, felix-1 is ready for tests.
			Eventually(func() error {
				for i := range []int{0, 1} {
					out, err := felixes[i].ExecOutput("ip", "link")
					if err != nil {
						return err
					}
					if strings.Contains(out, wireguardInterfaceNameDefault) {
						continue
					}
					return fmt.Errorf("felix-%d has no Wireguard device", i)
				}
				return nil
			}, "10s", "100ms").ShouldNot(HaveOccurred())
			for i := range []int{0, 1} {
				// Check the rule exists.
				Eventually(func() string {
					return getWireguardRoutingRule(felixes[i])
				}, "10s", "100ms").Should(MatchRegexp(fmt.Sprintf("\\d+:\\s+from all fwmark 0/0x\\d+ lookup \\d+")))
			}
			for i := range []int{0, 1} {
				// Check the route entry exists.
				Eventually(func() string {
					return getWireguardRouteEntry(felixes[i])
				}, "10s", "100ms").Should(ContainSubstring("dev wireguard.cali scope link"))
			}

			tcpdumps = nil
			for _, felix := range felixes {
				tcpdump := felix.AttachTCPDump("eth0")

				// tunnel packets.
				tunnelPackets01Pattern := fmt.Sprintf("IP %s\\.51820 > %s\\.51820: UDP", felixes[0].IP, felixes[1].IP)
				tcpdump.AddMatcher("numTunnelPackets01", regexp.MustCompile(tunnelPackets01Pattern))
				tunnelPackets10Pattern := fmt.Sprintf("IP %s\\.51820 > %s\\.51820: UDP", felixes[1].IP, felixes[0].IP)
				tcpdump.AddMatcher("numTunnelPackets10", regexp.MustCompile(tunnelPackets10Pattern))
				tunnelPackets02Pattern := fmt.Sprintf("IP %s\\.51820 > %s\\.51820: UDP", felixes[0].IP, felixes[2].IP)
				tcpdump.AddMatcher("numTunnelPackets02", regexp.MustCompile(tunnelPackets02Pattern))
				tunnelPackets20Pattern := fmt.Sprintf("IP %s\\.51820 > %s\\.51820: UDP", felixes[2].IP, felixes[0].IP)
				tcpdump.AddMatcher("numTunnelPackets20", regexp.MustCompile(tunnelPackets20Pattern))
				// direct workload packets.
				outWorkloadPacketsPattern := fmt.Sprintf("IP %s\\.\\d+ > %s\\.\\d+:", wls[0].IP, wls[2].IP)
				tcpdump.AddMatcher("numOutWorkloadPackets", regexp.MustCompile(outWorkloadPacketsPattern))
				inWorkloadPacketsPattern := fmt.Sprintf("IP %s\\.\\d+ > %s\\.\\d+:", wls[2].IP, wls[0].IP)
				tcpdump.AddMatcher("numInWorkloadPackets", regexp.MustCompile(inWorkloadPacketsPattern))

				tcpdump.Start()
				tcpdumps = append(tcpdumps, tcpdump)
			}
		})

		It("transfer should be encrypted/plain between workloads on WireGuard enabled/disabled nodes", func() {
			cc.ExpectSome(wls[0], wls[1])
			cc.ExpectSome(wls[1], wls[0])
			cc.CheckConnectivity()

			By("verifying packets between felix-0 and felix-1 is encrypted")
			for i := range []int{0, 1} {
				Eventually(func() int {
					return tcpdumps[i].MatchCount("numTunnelPackets01")
				}, "10s", "100ms").Should(BeNumerically(">", 0))
				Eventually(func() int {
					return tcpdumps[i].MatchCount("numTunnelPackets10")
				}, "10s", "100ms").Should(BeNumerically(">", 0))
			}

			cc.ResetExpectations()

			cc.ExpectSome(wls[2], wls[0])
			cc.ExpectSome(wls[0], wls[2])
			cc.CheckConnectivity()

			By("verifying packets between felix-0 and felix-2 are not encrypted")
			for _, f := range []int{0, 2} {
				Eventually(func() int {
					return tcpdumps[f].MatchCount("numInWorkloadPackets")
				}, "10s", "100ms").Should(BeNumerically(">", 0))
				Eventually(func() int {
					return tcpdumps[f].MatchCount("numOutWorkloadPackets")
				}, "10s", "100ms").Should(BeNumerically(">", 0))
			}
		})

		It("WireGuard should be used for host to workload connections on WireGuard enabled nodes", func() {
			cc.ExpectSome(felixes[0], wls[1])
			cc.CheckConnectivity()

			By("verifying packets between felix-0 and felix-1 is encrypted")
			for _, i := range []int{0, 1} {
				Eventually(func() int {
					return tcpdumps[i].MatchCount("numTunnelPackets01")
				}, "10s", "100ms").Should(BeNumerically(">", 0))
				Eventually(func() int {
					return tcpdumps[i].MatchCount("numTunnelPackets10")
				}, "10s", "100ms").Should(BeNumerically(">", 0))
			}
		})

		It("WireGuard should not be used for host to workload connections when WireGuard disabled on either node", func() {
			cc.ExpectSome(felixes[0], wls[2])
			cc.ExpectSome(felixes[2], wls[0])
			cc.CheckConnectivity()

			By("verifying packets between felix-0 and felix-2 are not encrypted")
			for _, i := range []int{0, 2} {
				Eventually(func() int {
					return tcpdumps[i].MatchCount("numTunnelPackets02")
				}, "10s", "100ms").Should(BeNumerically("==", 0))
				Eventually(func() int {
					return tcpdumps[i].MatchCount("numTunnelPackets20")
				}, "10s", "100ms").Should(BeNumerically("==", 0))
			}
		})
	})
})

// Setup cluster topology options.
// mainly, enable Wireguard with delayed start option.
func wireguardTopologyOptions() infrastructure.TopologyOptions {
	topologyOptions := infrastructure.DefaultTopologyOptions()

	// Waiting for calico-node to be ready.
	topologyOptions.DelayFelixStart = true
	// Wireguard doesn't support IPv6, disable it.
	topologyOptions.EnableIPv6 = false
	// Assigning workload IPs using IPAM API.
	topologyOptions.IPIPRoutesEnabled = false
	// Indicate wireguard is enabled
	topologyOptions.WireguardEnabled = true

	// Enable Wireguard.
	felixConfig := api.NewFelixConfiguration()
	felixConfig.SetName("default")
	enabled := true
	felixConfig.Spec.WireguardEnabled = &enabled
	topologyOptions.InitialFelixConfiguration = felixConfig

	// Debugging.
	//topologyOptions.ExtraEnvVars["FELIX_DebugUseShortPollIntervals"] = "true"
	//topologyOptions.FelixLogSeverity = "debug"

	return topologyOptions
}

func enableWireguard(client clientv3.Interface) {
	updateWireguardEnabledConfig(client, true)
}

func disableWireguard(client clientv3.Interface) {
	updateWireguardEnabledConfig(client, false)
}

func updateWireguardEnabledConfig(client clientv3.Interface, value bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	felixConfig, err := client.FelixConfigurations().Get(ctx, "default", options.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	felixConfig.Spec.WireguardEnabled = &value
	felixConfig, err = client.FelixConfigurations().Update(ctx, felixConfig, options.SetOptions{})
	Expect(err).NotTo(HaveOccurred())
}

func getWireguardRoutingRule(felix *infrastructure.Felix) string {
	rule, err := felix.ExecOutput("ip", "rule", "show", "pref", wireguardRoutingRulePriorityDefault)
	Expect(err).NotTo(HaveOccurred())
	return strings.TrimSpace(rule)
}

func getWireguardRouteEntry(felix *infrastructure.Felix) string {
	rule := getWireguardRoutingRule(felix)

	// Get route table index from rule.
	routingRuleRegExp := regexp.MustCompile(`\d+$`)
	tableId := routingRuleRegExp.FindString(rule)
	if tableId == "" {
		return ""
	}

	// Check route table entry.
	routes, err := felix.ExecOutput("ip", "route", "show", "table", tableId)
	Expect(err).NotTo(HaveOccurred())

	return routes
}

func disableWireguardForFelix(client clientv3.Interface, felixName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	felixConfig := api.NewFelixConfiguration()
	felixConfig.SetName(felixName)
	disabled := false
	felixConfig.Spec.WireguardEnabled = &disabled
	felixConfig, err := client.FelixConfigurations().Create(ctx, felixConfig, options.SetOptions{})
	Expect(err).NotTo(HaveOccurred())
}

func createWorkloadWithAssignedIP(
	infra *infrastructure.DatastoreInfra,
	client *clientv3.Interface,
	wlIP, wlName string,
	felix *infrastructure.Felix) *workload.Workload {

	err := (*client).IPAM().AssignIP(utils.Ctx, ipam.AssignIPArgs{
		IP:       net.MustParseIP(wlIP),
		HandleID: &wlName,
		Attrs: map[string]string{
			ipam.AttributeNode: felix.Hostname,
		},
		Hostname: felix.Hostname,
	})
	Expect(err).NotTo(HaveOccurred())

	wl := workload.Run(felix, wlName, "default", wlIP, "8055", "tcp")
	wl.ConfigureInInfra(*infra)

	return wl
}
