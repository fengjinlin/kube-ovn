package daemon

import (
	"fmt"
	"net"
	"strings"

	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/utils/sysctl"
	"github.com/vishvananda/netlink"
	"k8s.io/klog/v2"

	"github.com/fengjinlin/kube-ovn/pkg/cni/request"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/ovs"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type PodPort struct {
	VSwitchClient ovs.VSwitchClientInterface
	OvsWorker     OvsWorker

	PodName string
	PodNS   string

	routes []request.Route

	Provider string

	NetNS       string
	ContainerID string
	VfDriver    string

	IfaceName        string
	IfaceID          string
	IPAddr           string
	Mac              string
	MTU              int
	Gateway          string
	GatewayCheckMode int

	DeviceID string
	PortType string

	IsDefaultRoute     bool
	IsDetectIPConflict bool

	IngressRate string
	EgressRate  string
	Latency     string
	Limit       string
	Loss        string
	Jitter      string

	hostNicName      string
	containerNicName string
}

func (pp *PodPort) Install() ([]request.Route, error) {
	klog.Infof("configure pod port: %+v", pp)

	var (
		routes []request.Route
		err    error
	)

	switch pp.PortType {
	case consts.PortTypeVethPair:
		if err = pp.setupVethPair(); err != nil {
			klog.Errorf("failed to setup veth pair: %v", err)
			return nil, err
		}
		if err = pp.createPodPort(); err != nil {
			klog.Errorf("failed to create pod port: %v", err)
			return nil, err
		}
		if err = pp.configureHostNic(); err != nil {
			klog.Errorf("failed to configure host nic: %v", err)
			return nil, err
		}
		if routes, err = pp.configureContainerNic(); err != nil {
			klog.Errorf("failed to configure container nic: %v", err)
			return nil, err
		}

	default:
		return nil, fmt.Errorf("invalid port type: %s", pp.PortType)
	}

	if err = pp.OvsWorker.SetInterfaceBandwidth(pp.PodName, pp.PodNS, pp.IfaceID, pp.EgressRate, pp.IngressRate); err != nil {
		return nil, err
	}

	//if err = ovs.SetNetemQos(podName, podNamespace, ifaceID, latency, limit, loss, jitter); err != nil {
	//	return nil, err
	//}

	return routes, err
}

func (pp *PodPort) createPodPort() error {
	var (
		err error

		podName = pp.PodName
		podNS   = pp.PodNS
		netNS   = pp.NetNS
		ipAddr  = utils.GetIPWithoutMask(pp.IPAddr)
	)

	if err = pp.VSwitchClient.CleanDuplicateInterface(pp.IfaceID, pp.hostNicName); err != nil {
		klog.Errorf("failed to clean duplicate port: %v", err)
		return err
	}
	// Add veth pair host endpoint to ovs port
	externalIds := map[string]string{
		consts.ExternalIDsKeyIfaceID:  pp.IfaceID,
		consts.ExternalIDsKeyVendor:   consts.CniVendorName,
		consts.ExternalIDsKeyPod:      podName,
		consts.ExternalIDsKeyPodNS:    podNS,
		consts.ExternalIDsKeyIP:       ipAddr,
		consts.ExternalIDsKeyPodNetNS: netNS,
	}
	err = pp.VSwitchClient.CreatePort(consts.DefaultBridgeName, pp.hostNicName, pp.hostNicName, "", externalIds)
	if err != nil {
		return fmt.Errorf("failed to add nic to ovs: %v", err)
	}

	return nil
}

func (pp *PodPort) configureHostNic() error {
	var (
		nicName = pp.hostNicName
	)
	hostLink, err := netlink.LinkByName(nicName)
	if err != nil {
		return fmt.Errorf("can not find host nic %s: %v", nicName, err)
	}

	if hostLink.Attrs().OperState != netlink.OperUp {
		if err = netlink.LinkSetUp(hostLink); err != nil {
			return fmt.Errorf("can not set host nic %s up: %v", nicName, err)
		}
	}
	if err = netlink.LinkSetTxQLen(hostLink, 1000); err != nil {
		return fmt.Errorf("can not set host nic %s qlen: %v", nicName, err)
	}
	return nil
}

func (pp *PodPort) configureContainerNic() ([]request.Route, error) {
	var (
		finalRoutes []request.Route
		err         error

		nicName = pp.containerNicName
		nicType = pp.PortType
		ifName  = pp.IfaceName
		ipAddr  = pp.IPAddr

		netNS ns.NetNS
	)

	netNS, err = ns.GetNS(pp.NetNS)
	if err != nil {
		return nil, fmt.Errorf("failed to open netns %q: %v", pp.NetNS, err)
	}

	containerLink, err := netlink.LinkByName(nicName)
	if err != nil {
		return nil, fmt.Errorf("can not find container nic %s: %v", nicName, err)
	}

	// Set link alias to its origin link name for fastpath to recognize and bypass netfilter
	if err = netlink.LinkSetAlias(containerLink, nicName); err != nil {
		klog.Errorf("failed to set link alias for container nic %s: %v", nicName, err)
		return nil, err
	}

	if err = netlink.LinkSetNsFd(containerLink, int(netNS.Fd())); err != nil {
		return nil, fmt.Errorf("failed to move link to netns: %v", err)
	}

	err = ns.WithNetNSPath(netNS.Path(), func(_ ns.NetNS) error {
		if nicType != consts.PortTypeInternal {
			if err = netlink.LinkSetName(containerLink, ifName); err != nil {
				return err
			}
		}

		if utils.CheckProtocol(ipAddr) == utils.ProtocolDual || utils.CheckProtocol(ipAddr) == utils.ProtocolIPv6 {
			// For docker version >=17.x the "none" network will disable ipv6 by default.
			// We have to enable ipv6 here to add v6 address and gateway.
			// See https://github.com/containernetworking/cni/issues/531
			value, err := sysctl.Sysctl("net.ipv6.conf.all.disable_ipv6")
			if err != nil {
				return fmt.Errorf("failed to get sysctl net.ipv6.conf.all.disable_ipv6: %v", err)
			}
			if value != "0" {
				if _, err = sysctl.Sysctl("net.ipv6.conf.all.disable_ipv6", "0"); err != nil {
					return fmt.Errorf("failed to enable ipv6 on all nic: %v", err)
				}
			}
		}

		if nicType == consts.PortTypeInternal {
			//if err = addAdditionalNic(ifName); err != nil {
			//	return err
			//}
			//if err = configureAdditionalNic(ifName, ipAddr); err != nil {
			//	return err
			//}
			//if err = configureNic(nicName, ipAddr, macAddr, mtu, detectIPConflict); err != nil {
			//	return err
			//}
		} else {
			if err = configureNic(ifName, ipAddr, pp.Mac, pp.MTU, pp.IsDetectIPConflict); err != nil {
				return err
			}
		}

		if pp.IsDefaultRoute {
			// Only eth0 requires the default route and gateway
			containerGw := pp.Gateway

			for _, gw := range strings.Split(containerGw, ",") {
				if err = netlink.RouteReplace(&netlink.Route{
					LinkIndex: containerLink.Attrs().Index,
					Scope:     netlink.SCOPE_UNIVERSE,
					Gw:        net.ParseIP(gw),
				}); err != nil {
					return fmt.Errorf("failed to configure default gateway %s: %v", gw, err)
				}
			}
		}

		for _, r := range pp.routes {
			var dst *net.IPNet
			if r.Destination != "" {
				if _, dst, err = net.ParseCIDR(r.Destination); err != nil {
					klog.Errorf("invalid route destination %s: %v", r.Destination, err)
					continue
				}
			}

			var gw net.IP
			if r.Gateway != "" {
				if gw = net.ParseIP(r.Gateway); gw == nil {
					klog.Errorf("invalid route gateway %s", r.Gateway)
					continue
				}
			}

			route := &netlink.Route{
				Dst:       dst,
				Gw:        gw,
				LinkIndex: containerLink.Attrs().Index,
			}
			if err = netlink.RouteReplace(route); err != nil {
				klog.Errorf("failed to add route %+v: %v", r, err)
			}
		}

		linkRoutes, err := netlink.RouteList(containerLink, netlink.FAMILY_ALL)
		if err != nil {
			return fmt.Errorf("failed to get routes on interface %s: %v", ifName, err)
		}

		for _, r := range linkRoutes {
			if r.Family != netlink.FAMILY_V4 && r.Family != netlink.FAMILY_V6 {
				continue
			}
			if r.Dst == nil && r.Gw == nil {
				continue
			}
			if r.Dst != nil && r.Dst.IP.IsLinkLocalUnicast() {
				if _, bits := r.Dst.Mask.Size(); bits == net.IPv6len*8 {
					// skip fe80::/10
					continue
				}
			}

			var route request.Route
			if r.Dst != nil {
				route.Destination = r.Dst.String()
			}
			if r.Gw != nil {
				route.Gateway = r.Gw.String()
			}
			finalRoutes = append(finalRoutes, route)
		}

		gwCheckMode := pp.GatewayCheckMode
		if gwCheckMode != consts.GatewayCheckModeDisable {
			var (
				underlayGateway = gwCheckMode == consts.GatewayCheckModeArp || gwCheckMode == consts.GatewayCheckModeArpNotConcerned
				interfaceName   = nicName
			)

			if nicType != consts.PortTypeInternal {
				interfaceName = ifName
			}

			return checkGatewayReady(gwCheckMode, interfaceName, ipAddr, pp.Gateway, underlayGateway, true)
		}

		return nil
	})

	return finalRoutes, err
}

func (pp *PodPort) setupVethPair() error {
	var err error
	pp.generateNicName()
	// Create a veth pair, put one end to container ,the other to ovs port
	// NOTE: DO NOT use ovs internal type interface for container.
	// Kubernetes will detect 'eth0' nic in pod, so the nic name in pod must be 'eth0'.
	// When renaming internal interface to 'eth0', ovs will delete and recreate this interface.
	veth := netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: pp.hostNicName}, PeerName: pp.containerNicName}
	if pp.MTU > 0 {
		veth.MTU = pp.MTU
	}
	if err = netlink.LinkAdd(&veth); err != nil {
		if err := netlink.LinkDel(&veth); err != nil {
			klog.Errorf("failed to delete veth %v", err)
			return err
		}
		return fmt.Errorf("failed to create veth for %v", err)
	}
	return nil
}

func (pp *PodPort) generateNicName() {
	var (
		containerID = pp.ContainerID
		ifName      = pp.IfaceName

		hostNicName, containerNicName string
	)

	switch {
	case ifName == "eth0":
		hostNicName = fmt.Sprintf("%s_h", containerID[0:12])
		containerNicName = fmt.Sprintf("%s_c", containerID[0:12])

	case strings.HasPrefix(ifName, "pod") && len(ifName) == 14:
		ifName = ifName[3 : len(ifName)-4]
		hostNicName = fmt.Sprintf("%s_%s_h", containerID[0:12-len(ifName)], ifName)
		containerNicName = fmt.Sprintf("%s_%s_c", containerID[0:12-len(ifName)], ifName)

	default:
		hostNicName = fmt.Sprintf("%s_%s_h", containerID[0:12-len(ifName)], ifName)
		containerNicName = fmt.Sprintf("%s_%s_c", containerID[0:12-len(ifName)], ifName)
	}

	pp.hostNicName = hostNicName
	pp.containerNicName = containerNicName
}

func (pp *PodPort) configureVethPair() ([]request.Route, error) {

	return nil, nil
}

func (pp *PodPort) Uninstall() error {
	pp.generateNicName()
	var nicName string
	if pp.PortType == consts.PortTypeInternal {
		nicName = pp.containerNicName
	} else {
		nicName = pp.hostNicName
	}

	// Remove ovs port
	err := pp.VSwitchClient.DeletePort("br-int", nicName)
	if err != nil {
		return fmt.Errorf("failed to delete ovs port %v: %b", nicName, err)
	}

	//if err = ovs.ClearPodBandwidth(podName, podNamespace, ""); err != nil {
	//	return err
	//}
	//if err = ovs.ClearHtbQosQueue(podName, podNamespace, ""); err != nil {
	//	return err
	//}

	if pp.DeviceID == "" {
		hostLink, err := netlink.LinkByName(nicName)
		if err != nil {
			// If link already not exists, return quietly
			// E.g. Internal port had been deleted by Remove ovs port previously
			if _, ok := err.(netlink.LinkNotFoundError); ok {
				return nil
			}
			return fmt.Errorf("find host link %s failed %v", nicName, err)
		}

		hostLinkType := hostLink.Type()
		// Sometimes no deviceID input for vf nic, avoid delete vf nic.
		if hostLinkType == "veth" {
			if err = netlink.LinkDel(hostLink); err != nil {
				return fmt.Errorf("delete host link %s failed %v", hostLink, err)
			}
		}
		//} else if pciAddrRegexp.MatchString(deviceID) {
		//	// Ret VF index from PCI
		//	vfIndex, err := sriovnet.GetVfIndexByPciAddress(deviceID)
		//	if err != nil {
		//		klog.Errorf("failed to get vf %s index, %v", deviceID, err)
		//		return err
		//	}
		//	if err = setVfMac(deviceID, vfIndex, "00:00:00:00:00:00"); err != nil {
		//		return err
		//	}
	}
	return nil
}
