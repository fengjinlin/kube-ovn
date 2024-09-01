package daemon

import (
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/utils/ipset"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/klog/v2"
	"k8s.io/utils/exec"

	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

func (c *Controller) initIPSets() error {
	c.ipSetsController = &IPSetsController{
		ipSets: ipset.New(exec.New()),
	}

	return nil
}

func (c *Controller) setUpIPSets() error {
	var protocols []string
	switch c.protocol {
	case utils.ProtocolDual:
		protocols = []string{utils.ProtocolIPv4, utils.ProtocolIPv6}
	case utils.ProtocolIPv4, utils.ProtocolIPv6:
		protocols = []string{c.protocol}
	default:
		return fmt.Errorf("failed to set up ipsets: protocol `%s` not supported", c.protocol)
	}

	allSubnets, err := c.subnetsLister.List(labels.Everything())
	if err != nil {
		klog.Error(err)
		return err
	}

	allNodes, err := c.nodesLister.List(labels.Everything())
	if err != nil {
		klog.Error(err)
		return err
	}

	for _, protocol := range protocols {
		var (
			ipSetProtocol = ""
			hashFamily    = ipset.ProtocolFamilyIPV4
		)
		if protocol == utils.ProtocolIPv6 {
			ipSetProtocol = "6-"
			hashFamily = ipset.ProtocolFamilyIPV6
		}

		// services
		var serviceCIDRs []string
		for _, cidr := range strings.Split(c.config.ServiceClusterIPRange, ",") {
			if utils.CheckProtocol(cidr) == protocol {
				serviceCIDRs = append(serviceCIDRs, cidr)
			}
		}
		c.ipSetsController.AddOrUpdateIPSet(
			&ipset.IPSet{
				Name:       fmt.Sprintf(consts.IPSetServiceTpl, ipSetProtocol),
				SetType:    ipset.HashNet,
				HashFamily: hashFamily,
			},
			serviceCIDRs,
		)

		// subnets
		var subnetCIDRs []string
		var subnetCIDRsForNat []string
		var subnetCIDRsForDistributedGw []string
		for _, subnet := range allSubnets {
			// subnets under default vpc
			if subnet.Spec.Vpc == c.config.ClusterRouter &&
				(subnet.Spec.Vlan == "" || subnet.Spec.LogicalGateway) &&
				subnet.Spec.CIDRBlock != "" &&
				(subnet.Spec.Protocol == protocol || subnet.Spec.Protocol == utils.ProtocolDual) {
				cidr := utils.FilterCIDR(subnet.Spec.CIDRBlock, protocol)
				if cidr != "" {
					// all subnets
					subnetCIDRs = append(subnetCIDRs, cidr)

					// subnets need nat
					if subnet.DeletionTimestamp == nil &&
						subnet.Spec.NatOutgoing {
						subnetCIDRsForNat = append(subnetCIDRsForNat, cidr)
					}

					// subnets distribute gateway
					if subnet.Spec.GatewayType == consts.GatewayTypeDistributed {
						subnetCIDRsForDistributedGw = append(subnetCIDRsForDistributedGw, cidr)
					}
				}
			}
		}
		c.ipSetsController.AddOrUpdateIPSet(
			&ipset.IPSet{
				Name:       fmt.Sprintf(consts.IPSetSubnetTpl, ipSetProtocol),
				SetType:    ipset.HashNet,
				HashFamily: hashFamily,
			},
			subnetCIDRs,
		)
		c.ipSetsController.AddOrUpdateIPSet(
			&ipset.IPSet{
				Name:       fmt.Sprintf(consts.IPSetLocalPodIPTpl, ipSetProtocol),
				SetType:    ipset.HashIP,
				HashFamily: hashFamily,
			},
			nil,
		)
		c.ipSetsController.AddOrUpdateIPSet(
			&ipset.IPSet{
				Name:       fmt.Sprintf(consts.IPSetSubnetNatTpl, ipSetProtocol),
				SetType:    ipset.HashNet,
				HashFamily: hashFamily,
			},
			subnetCIDRsForNat,
		)
		c.ipSetsController.AddOrUpdateIPSet(
			&ipset.IPSet{
				Name:       fmt.Sprintf(consts.IPSetSubnetDistributedGwTpl, ipSetProtocol),
				SetType:    ipset.HashNet,
				HashFamily: hashFamily,
			},
			subnetCIDRsForDistributedGw,
		)

		// nodes
		var otherNodeIPs []string
		for _, node := range allNodes {
			if node.Name == c.config.NodeName {
				continue
			}

			for _, addr := range node.Status.Addresses {
				if addr.Type == corev1.NodeInternalIP && utils.CheckProtocol(addr.Address) == protocol {
					otherNodeIPs = append(otherNodeIPs, addr.Address)
				}
			}
		}
		c.ipSetsController.AddOrUpdateIPSet(
			&ipset.IPSet{
				Name:       fmt.Sprintf(consts.IPSetOtherNodeTpl, ipSetProtocol),
				SetType:    ipset.HashNet,
				HashFamily: hashFamily,
			},
			otherNodeIPs,
		)
	}

	return nil
}

type IPSetsController struct {
	ipSets ipset.Interface
}

func (c *IPSetsController) AddOrUpdateIPSet(set *ipset.IPSet, elements []string) {
	if err := c.ipSets.CreateSet(set, true); err != nil {
		klog.Error(err)
		return
	}
	entries, err := c.ipSets.ListEntries(set.Name)
	if err != nil {
		klog.Error(err)
		return
	}
	entryMap := make(map[string]int, len(entries))
	for _, entry := range entries {
		entryMap[entry] = 0
	}
	for _, element := range elements {
		if _, ok := entryMap[element]; ok {
			delete(entryMap, element)
		} else {
			_ = c.ipSets.AddEntry(element, set, true)
		}
	}
	for entry := range entryMap {
		_ = c.ipSets.DelEntry(entry, set.Name)
	}
}

func (c *IPSetsController) ipSetExists(name string) (bool, error) {
	setList, err := c.ipSets.ListSets()
	if err != nil {
		klog.Error(err)
		return false, err
	}
	for _, set := range setList {
		if set == name {
			return true, err
		}
	}

	return false, fmt.Errorf("ipset %s not found", name)
}

func (c *Controller) ipSetExists(name string) (bool, error) {
	return c.ipSetsController.ipSetExists(name)
}
