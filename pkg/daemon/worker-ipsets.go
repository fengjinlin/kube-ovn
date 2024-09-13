package daemon

import (
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformer "k8s.io/client-go/informers/core/v1"
	corelister "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/exec"

	ovninformer "github.com/fengjinlin/kube-ovn/pkg/client/informers/externalversions/kubeovn/v1"
	ovnlister "github.com/fengjinlin/kube-ovn/pkg/client/listers/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
	"github.com/fengjinlin/kube-ovn/pkg/utils/ipset"
)

type IPSetsWorker interface {
	Run(stopCh <-chan struct{})
	IPSetExists(name string) (bool, error)
}

func NewIPSetsWorker(config *Configuration,
	subnetInformer ovninformer.SubnetInformer,
	nodeInformer coreinformer.NodeInformer,
	protocol string) (IPSetsWorker, error) {
	return &ipSetsWorker{
		config:        config,
		subnetsLister: subnetInformer.Lister(),
		nodesLister:   nodeInformer.Lister(),
		protocol:      protocol,
		ipSets:        ipset.New(exec.New()),
	}, nil
}

type ipSetsWorker struct {
	config *Configuration

	subnetsLister ovnlister.SubnetLister
	nodesLister   corelister.NodeLister

	protocol string

	ipSets ipset.Interface
}

func (w *ipSetsWorker) Run(stopCh <-chan struct{}) {
	go wait.Until(func() {
		if err := w.setUpIPSets(); err != nil {
			klog.Error("failed to setup ipSets", err)
		}
	}, 3*time.Second, stopCh)

	<-stopCh
	klog.Info("stopping ipSets manager")
}

func (w *ipSetsWorker) setUpIPSets() error {
	var protocols []string
	switch w.protocol {
	case utils.ProtocolDual:
		protocols = []string{utils.ProtocolIPv4, utils.ProtocolIPv6}
	case utils.ProtocolIPv4, utils.ProtocolIPv6:
		protocols = []string{w.protocol}
	default:
		return fmt.Errorf("failed to set up ipsets: protocol `%s` not supported", w.protocol)
	}

	allSubnets, err := w.subnetsLister.List(labels.Everything())
	if err != nil {
		klog.Error(err)
		return err
	}

	allNodes, err := w.nodesLister.List(labels.Everything())
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
		for _, cidr := range strings.Split(w.config.ServiceClusterIPRange, ",") {
			if utils.CheckProtocol(cidr) == protocol {
				serviceCIDRs = append(serviceCIDRs, cidr)
			}
		}
		w.addOrUpdateIPSet(
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
			if subnet.Spec.Vpc == w.config.ClusterRouter &&
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
		w.addOrUpdateIPSet(
			&ipset.IPSet{
				Name:       fmt.Sprintf(consts.IPSetSubnetTpl, ipSetProtocol),
				SetType:    ipset.HashNet,
				HashFamily: hashFamily,
			},
			subnetCIDRs,
		)
		w.addOrUpdateIPSet(
			&ipset.IPSet{
				Name:       fmt.Sprintf(consts.IPSetLocalPodIPTpl, ipSetProtocol),
				SetType:    ipset.HashIP,
				HashFamily: hashFamily,
			},
			nil,
		)
		w.addOrUpdateIPSet(
			&ipset.IPSet{
				Name:       fmt.Sprintf(consts.IPSetSubnetNatTpl, ipSetProtocol),
				SetType:    ipset.HashNet,
				HashFamily: hashFamily,
			},
			subnetCIDRsForNat,
		)
		w.addOrUpdateIPSet(
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
			if node.Name == w.config.NodeName {
				continue
			}

			for _, addr := range node.Status.Addresses {
				if addr.Type == corev1.NodeInternalIP && utils.CheckProtocol(addr.Address) == protocol {
					otherNodeIPs = append(otherNodeIPs, addr.Address)
				}
			}
		}
		w.addOrUpdateIPSet(
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

func (w *ipSetsWorker) addOrUpdateIPSet(set *ipset.IPSet, elements []string) {
	if err := w.ipSets.CreateSet(set, true); err != nil {
		klog.Error(err)
		return
	}
	entries, err := w.ipSets.ListEntries(set.Name)
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
			_ = w.ipSets.AddEntry(element, set, true)
		}
	}
	for entry := range entryMap {
		_ = w.ipSets.DelEntry(entry, set.Name)
	}
}

func (w *ipSetsWorker) IPSetExists(name string) (bool, error) {
	setList, err := w.ipSets.ListSets()
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
