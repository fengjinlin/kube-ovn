package daemon

import (
	"fmt"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/utils/sysctl"
	"github.com/emicklei/go-restful/v3"
	kubeovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	clientset "github.com/fengjinlin/kube-ovn/pkg/client/clientset/versioned"
	"github.com/fengjinlin/kube-ovn/pkg/cni/request"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/translator"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
	"github.com/vishvananda/netlink"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"net"
	"net/http"
	"strings"
	"time"
)

const (
	gatewayModeDisabled = iota
	gatewayCheckModePing
	gatewayCheckModeArping
	gatewayCheckModePingNotConcerned
	gatewayCheckModeArpingNotConcerned

	gatewayCheckMaxRetry = 200
)

type cniServerHandler struct {
	Config        *Configuration
	KubeClient    kubernetes.Interface
	KubeOvnClient clientset.Interface
	Controller    *Controller
}

func createCniServerHandler(config *Configuration, controller *Controller) *cniServerHandler {
	return &cniServerHandler{
		KubeClient:    config.KubeClient,
		KubeOvnClient: config.KubeOvnClient,
		Config:        config,
		Controller:    controller,
	}
}

func (csh cniServerHandler) handleAdd(req *restful.Request, resp *restful.Response) {
	podRequest := request.CniRequest{}
	if err := req.ReadEntity(&podRequest); err != nil {
		errMsg := fmt.Errorf("parse add request failed %v", err)
		klog.Error(errMsg)
		if err := resp.WriteHeaderAndEntity(http.StatusBadRequest, request.CniResponse{Err: errMsg.Error()}); err != nil {
			klog.Errorf("failed to write response, %v", err)
		}
		return
	}

	klog.Infof("add port request: %+v", podRequest)

	podSubnet, exist := csh.providerExists(podRequest.Provider)
	if !exist {
		errMsg := fmt.Errorf("provider %s not bind to any subnet", podRequest.Provider)
		klog.Error(errMsg)
		if err := resp.WriteHeaderAndEntity(http.StatusBadRequest, request.CniResponse{Err: errMsg.Error()}); err != nil {
			klog.Errorf("failed to write response, %v", err)
		}
		return
	}

	ifName := podRequest.IfName
	if ifName == "" {
		ifName = "eth0"
	}

	var gatewayCheckMode int
	var macAddr, ip, ipAddr, cidr, gw, subnet, ingress, egress, nicType, podNicName, latency, limit, loss, jitter, u2oInterconnectionIP string
	var routes []request.Route
	var isDefaultRoute bool
	var pod *v1.Pod
	var err error
	for i := 0; i < 20; i++ {
		if pod, err = csh.Controller.podsLister.Pods(podRequest.PodNamespace).Get(podRequest.PodName); err != nil {
			errMsg := fmt.Errorf("get pod %s/%s failed %v", podRequest.PodNamespace, podRequest.PodName, err)
			klog.Error(errMsg)
			if err := resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: errMsg.Error()}); err != nil {
				klog.Errorf("failed to write response, %v", err)
			}
			return
		}
		if pod.Annotations[fmt.Sprintf(consts.AnnotationAllocatedTemplate, podRequest.Provider)] != "true" {
			klog.Infof("wait address for pod %s/%s provider %s", podRequest.PodNamespace, podRequest.PodName, podRequest.Provider)
			// wait controller assign an address
			time.Sleep(1 * time.Second)
			continue
		}

		switch pod.Annotations[fmt.Sprintf(consts.AnnotationDefaultRouteTemplate, podRequest.Provider)] {
		case "true":
			isDefaultRoute = true
		case "false":
			isDefaultRoute = false
		default:
			isDefaultRoute = ifName == "eth0"
		}

		if isDefaultRoute &&
			pod.Annotations[fmt.Sprintf(consts.AnnotationRoutedTemplate, podRequest.Provider)] != "true" &&
			strings.HasSuffix(podRequest.Provider, consts.OvnProviderName) {
			klog.Infof("wait route ready for pod %s/%s provider %s", podRequest.PodNamespace, podRequest.PodName, podRequest.Provider)
			//cniWaitRouteResult.WithLabelValues(nodeName).Inc()
			time.Sleep(1 * time.Second)
			continue
		}

		//if err := utils.ValidatePodNetwork(pod.Annotations); err != nil {
		//	klog.Errorf("validate pod %s/%s failed, %v", podRequest.PodNamespace, podRequest.PodName, err)
		//	// wait controller assign an address
		//	time.Sleep(1 * time.Second)
		//	continue
		//}
		ip = pod.Annotations[fmt.Sprintf(consts.AnnotationIPAddressTemplate, podRequest.Provider)]
		cidr = pod.Annotations[fmt.Sprintf(consts.AnnotationCidrTemplate, podRequest.Provider)]
		gw = pod.Annotations[fmt.Sprintf(consts.AnnotationGatewayTemplate, podRequest.Provider)]
		macAddr = pod.Annotations[fmt.Sprintf(consts.AnnotationMacAddressTemplate, podRequest.Provider)]
		subnet = pod.Annotations[fmt.Sprintf(consts.AnnotationSubnetTemplate, podRequest.Provider)]
		//ingress = pod.Annotations[fmt.Sprintf(util.IngressRateAnnotationTemplate, podRequest.Provider)]
		//egress = pod.Annotations[fmt.Sprintf(util.EgressRateAnnotationTemplate, podRequest.Provider)]
		//latency = pod.Annotations[fmt.Sprintf(util.NetemQosLatencyAnnotationTemplate, podRequest.Provider)]
		//limit = pod.Annotations[fmt.Sprintf(util.NetemQosLimitAnnotationTemplate, podRequest.Provider)]
		//loss = pod.Annotations[fmt.Sprintf(util.NetemQosLossAnnotationTemplate, podRequest.Provider)]
		//jitter = pod.Annotations[fmt.Sprintf(util.NetemQosJitterAnnotationTemplate, podRequest.Provider)]
		//providerNetwork = pod.Annotations[fmt.Sprintf(consts.ProviderNetworkTemplate, podRequest.Provider)]
		//vmName = pod.Annotations[fmt.Sprintf(util.VMAnnotationTemplate, podRequest.Provider)]
		ipAddr = utils.GetIPAddrWithMask(ip, cidr)
		//if s := pod.Annotations[fmt.Sprintf(utils.RoutesAnnotationTemplate, podRequest.Provider)]; s != "" {
		//	if err = json.Unmarshal([]byte(s), &routes); err != nil {
		//		errMsg := fmt.Errorf("invalid routes for pod %s/%s: %v", pod.Namespace, pod.Name, err)
		//		klog.Error(errMsg)
		//		if err = resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: errMsg.Error()}); err != nil {
		//			klog.Errorf("failed to write response: %v", err)
		//		}
		//		return
		//	}
		//}

		switch {
		case podRequest.DeviceID != "":
			nicType = consts.PortTypeOffload
		case podRequest.VhostUserSocketVolumeName != "":
			nicType = consts.PortTypeDpdk
			//if err = createShortSharedDir(pod, podRequest.VhostUserSocketVolumeName, csh.Config.KubeletDir); err != nil {
			//	klog.Error(err.Error())
			//	if err = resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: err.Error()}); err != nil {
			//		klog.Errorf("failed to write response: %v", err)
			//	}
			//	return
			//}
		default:
			nicType = pod.Annotations[fmt.Sprintf(consts.AnnotationPodNicTypeTemplate, podRequest.Provider)]
		}

		break
	}

	if pod == nil || pod.Annotations[fmt.Sprintf(consts.AnnotationAllocatedTemplate, podRequest.Provider)] != "true" {
		err := fmt.Errorf("no address allocated to pod %s/%s provider %s, please see kube-ovn-controller logs to find errors", pod.Namespace, pod.Name, podRequest.Provider)
		klog.Error(err)
		if err := resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: err.Error()}); err != nil {
			klog.Errorf("failed to write response, %v", err)
		}
		return
	}

	if isDefaultRoute &&
		pod.Annotations[fmt.Sprintf(consts.AnnotationRoutedTemplate, podRequest.Provider)] != "true" &&
		strings.HasSuffix(podRequest.Provider, consts.OvnProviderName) {
		err := fmt.Errorf("route is not ready for pod %s/%s provider %s, please see kube-ovn-controller logs to find errors",
			pod.Namespace, pod.Name, podRequest.Provider)
		klog.Error(err)
		if err := resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: err.Error()}); err != nil {
			klog.Errorf("failed to write response, %v", err)
		}
		return
	}

	if subnet == "" && podSubnet != nil {
		subnet = podSubnet.Name
	}

	//if err := csh.UpdateIPCr(podRequest, subnet, ip); err != nil {
	//	if err := resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: err.Error()}); err != nil {
	//		klog.Errorf("failed to write response, %v", err)
	//	}
	//	return
	//}

	routes = append(podRequest.Routes, routes...)
	if strings.HasSuffix(podRequest.Provider, consts.OvnProviderName) && subnet != "" {
		podSubnet, err := csh.Controller.subnetsLister.Get(subnet)
		if err != nil {
			errMsg := fmt.Errorf("failed to get subnet %s: %v", subnet, err)
			klog.Error(errMsg)
			if err = resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: errMsg.Error()}); err != nil {
				klog.Errorf("failed to write response: %v", err)
			}
			return
		}

		if podSubnet.Status.U2OInterconnectionIP == "" && podSubnet.Spec.U2OInterconnection {
			errMsg := fmt.Errorf("failed to generate u2o ip on subnet %s ", podSubnet.Name)
			klog.Error(errMsg)
			return
		}

		if podSubnet.Status.U2OInterconnectionIP != "" && podSubnet.Spec.U2OInterconnection {
			u2oInterconnectionIP = podSubnet.Status.U2OInterconnectionIP
		}

		subnetHasVlan := podSubnet.Spec.Vlan != ""
		detectIPConflict := csh.Config.EnableArpDetectIPConflict && subnetHasVlan

		var mtu int
		if podSubnet.Spec.Mtu > 0 {
			mtu = int(podSubnet.Spec.Mtu)
		} else {
			mtu = csh.Config.MTU
		}

		klog.Infof("create container interface %s mac %s, ip %s, cidr %s, gw %s, custom routes %v", ifName, macAddr, ipAddr, cidr, gw, routes)
		podNicName = ifName
		switch nicType {
		case consts.PortTypeInternal:
			//podNicName, routes, err = csh.configureNicWithInternalPort(podRequest.PodName, podRequest.PodNamespace, podRequest.Provider, podRequest.NetNs, podRequest.ContainerID, ifName, macAddr, mtu, ipAddr, gw, isDefaultRoute, detectIPConflict, routes, podRequest.DNS.Nameservers, podRequest.DNS.Search, ingress, egress, podRequest.DeviceID, nicType, latency, limit, loss, jitter, gatewayCheckMode, u2oInterconnectionIP)
		case consts.PortTypeDpdk:
			//err = csh.configureDpdkNic(podRequest.PodName, podRequest.PodNamespace, podRequest.Provider, podRequest.NetNs, podRequest.ContainerID, ifName, macAddr, mtu, ipAddr, gw, ingress, egress, getShortSharedDir(pod.UID, podRequest.VhostUserSocketVolumeName), podRequest.VhostUserSocketName)
			routes = nil
		default:
			routes, err = csh.configureNic(podRequest.PodName, podRequest.PodNamespace, podRequest.Provider, podRequest.NetNs, podRequest.ContainerID, podRequest.VfDriver, ifName, macAddr, mtu, ipAddr, gw, isDefaultRoute, detectIPConflict, routes, podRequest.DNS.Nameservers, podRequest.DNS.Search, ingress, egress, podRequest.DeviceID, nicType, latency, limit, loss, jitter, gatewayCheckMode, u2oInterconnectionIP)
		}
		if err != nil {
			errMsg := fmt.Errorf("configure nic failed %v", err)
			klog.Error(errMsg)
			if err := resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: errMsg.Error()}); err != nil {
				klog.Errorf("failed to write response, %v", err)
			}
			return
		}

		//ifaceID := translator.PodNameToPortName(podRequest.PodName, podRequest.PodNamespace, podRequest.Provider)
		//if err = ovs.ConfigInterfaceMirror(csh.Config.EnableMirror, pod.Annotations[fmt.Sprintf(util.MirrorControlAnnotationTemplate, podRequest.Provider)], ifaceID); err != nil {
		//	klog.Errorf("failed mirror to mirror0, %v", err)
		//	return
		//}

		//if err = csh.Controller.addEgressConfig(podSubnet, ip); err != nil {
		//	errMsg := fmt.Errorf("failed to add egress configuration: %v", err)
		//	klog.Error(errMsg)
		//	if err = resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: errMsg.Error()}); err != nil {
		//		klog.Errorf("failed to write response, %v", err)
		//	}
		//	return
		//}
	} else if len(routes) != 0 {
		hasDefaultRoute := make(map[string]bool, 2)
		for _, r := range routes {
			if r.Destination == "" {
				hasDefaultRoute[utils.CheckProtocol(r.Gateway)] = true
				continue
			}
			if _, cidr, err := net.ParseCIDR(r.Destination); err == nil {
				if ones, _ := cidr.Mask.Size(); ones == 0 {
					hasDefaultRoute[utils.CheckProtocol(r.Gateway)] = true
				}
			}
		}
		if len(hasDefaultRoute) != 0 {
			// remove existing default route so other CNI plugins, such as macvlan, can add the new default route correctly
			//if err = csh.removeDefaultRoute(podRequest.NetNs, hasDefaultRoute[kubeovnv1.ProtocolIPv4], hasDefaultRoute[kubeovnv1.ProtocolIPv6]); err != nil {
			//	errMsg := fmt.Errorf("failed to remove existing default route for interface %s of pod %s/%s: %v", podRequest.IfName, podRequest.PodNamespace, podRequest.PodName, err)
			//	klog.Error(errMsg)
			//	if err = resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: errMsg.Error()}); err != nil {
			//		klog.Errorf("failed to write response: %v", err)
			//	}
			//	return
			//}
		}
	}

	response := &request.CniResponse{
		Protocol:   utils.CheckProtocol(cidr),
		IPAddress:  ip,
		MacAddress: macAddr,
		CIDR:       cidr,
		PodNicName: podNicName,
		Routes:     routes,
	}
	if isDefaultRoute {
		response.Gateway = gw
	}
	if err := resp.WriteHeaderAndEntity(http.StatusOK, response); err != nil {
		klog.Errorf("failed to write response, %v", err)
	}

}

func (csh cniServerHandler) providerExists(provider string) (*kubeovnv1.Subnet, bool) {
	if provider == "" || strings.HasSuffix(provider, consts.OvnProviderName) {
		return nil, true
	}
	subnets, _ := csh.Controller.subnetsLister.List(labels.Everything())
	for _, subnet := range subnets {
		if subnet.Spec.Provider == provider {
			return subnet.DeepCopy(), true
		}
	}
	return nil, false
}

func (csh cniServerHandler) configureNic(podName, podNamespace, provider, netns, containerID, vfDriver, ifName, mac string, mtu int, ip, gateway string, isDefaultRoute, detectIPConflict bool, routes []request.Route, _, _ []string, ingress, egress, deviceID, nicType, latency, limit, loss, jitter string, gwCheckMode int, u2oInterconnectionIP string) ([]request.Route, error) {
	var err error
	var hostNicName, containerNicName string
	if deviceID == "" {
		hostNicName, containerNicName, err = setupVethPair(containerID, ifName, mtu)
		if err != nil {
			klog.Errorf("failed to create veth pair %v", err)
			return nil, err
		}
	} else {
		//hostNicName, containerNicName, err = setupSriovInterface(containerID, deviceID, vfDriver, ifName, mtu, mac)
		//if err != nil {
		//	klog.Errorf("failed to create sriov interfaces %v", err)
		//	return nil, err
		//}
	}

	if containerNicName == "" {
		return nil, nil
	}

	ipStr := utils.GetIPWithoutMask(ip)
	ifaceID := translator.PodNameToPortName(podName, podNamespace, provider)
	//ifaceID := ovs.PodNameToPortName(podName, podNamespace, provider)
	//ovs.CleanDuplicatePort(ifaceID, hostNicName)
	// Add veth pair host end to ovs port
	externalIds := map[string]string{
		"iface-id":      ifaceID,
		"vendor":        consts.CniVendorName,
		"pod_name":      podName,
		"pod_namespace": podNamespace,
		"ip":            ipStr,
		"pod_net_ns":    netns,
	}
	err = csh.Config.vSwitchClient.CreatePort(consts.DefaultBridgeName, hostNicName, hostNicName, "", externalIds)
	if err != nil {
		return nil, fmt.Errorf("failed to add nic to ovs: %v", err)
	}

	// lsp and container nic must use same mac address, otherwise ovn will reject these packets by default
	macAddr, err := net.ParseMAC(mac)
	if err != nil {
		return nil, fmt.Errorf("failed to parse mac %s %v", macAddr, err)
	}
	if err = configureHostNic(hostNicName); err != nil {
		return nil, err
	}
	//if err = ovs.SetInterfaceBandwidth(podName, podNamespace, ifaceID, egress, ingress); err != nil {
	//	return nil, err
	//}

	//if err = ovs.SetNetemQos(podName, podNamespace, ifaceID, latency, limit, loss, jitter); err != nil {
	//	return nil, err
	//}

	podNS, err := ns.GetNS(netns)
	if err != nil {
		return nil, fmt.Errorf("failed to open netns %q: %v", netns, err)
	}
	return configureContainerNic(csh.Config, containerNicName, ifName, ip, gateway, isDefaultRoute, detectIPConflict, routes, macAddr, podNS, mtu, nicType, gwCheckMode, u2oInterconnectionIP)
}

func configureContainerNic(config *Configuration, nicName, ifName, ipAddr, gateway string, isDefaultRoute, detectIPConflict bool, routes []request.Route, macAddr net.HardwareAddr, netns ns.NetNS, mtu int, nicType string, gwCheckMode int, u2oInterconnectionIP string) ([]request.Route, error) {
	containerLink, err := netlink.LinkByName(nicName)
	if err != nil {
		return nil, fmt.Errorf("can not find container nic %s: %v", nicName, err)
	}

	// Set link alias to its origin link name for fastpath to recognize and bypass netfilter
	if err := netlink.LinkSetAlias(containerLink, nicName); err != nil {
		klog.Errorf("failed to set link alias for container nic %s: %v", nicName, err)
		return nil, err
	}

	if err = netlink.LinkSetNsFd(containerLink, int(netns.Fd())); err != nil {
		return nil, fmt.Errorf("failed to move link to netns: %v", err)
	}

	var finalRoutes []request.Route
	err = ns.WithNetNSPath(netns.Path(), func(_ ns.NetNS) error {
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
			if err = configureNic(config, ifName, ipAddr, macAddr, mtu, detectIPConflict); err != nil {
				return err
			}
		}

		if isDefaultRoute {
			// Only eth0 requires the default route and gateway
			containerGw := gateway
			if u2oInterconnectionIP != "" {
				containerGw = u2oInterconnectionIP
			}

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

		for _, r := range routes {
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

		if gwCheckMode != gatewayModeDisabled {
			var (
				underlayGateway = gwCheckMode == gatewayCheckModeArping || gwCheckMode == gatewayCheckModeArpingNotConcerned
				interfaceName   = nicName
			)

			if nicType != consts.PortTypeInternal {
				interfaceName = ifName
			}

			if u2oInterconnectionIP != "" {
				//if err := checkGatewayReady(gwCheckMode, interfaceName, ipAddr, u2oInterconnectionIP, false, true); err != nil {
				//	return err
				//}
			}
			return checkGatewayReady(gwCheckMode, interfaceName, ipAddr, gateway, underlayGateway, true)
		}

		return nil
	})

	return finalRoutes, err
}

func checkGatewayReady(gwCheckMode int, intr, ipAddr, gateway string, underlayGateway, verbose bool) error {
	var err error

	if gwCheckMode == gatewayCheckModeArpingNotConcerned || gwCheckMode == gatewayCheckModePingNotConcerned {
		// ignore error if disableGatewayCheck=true
		if err = waitNetworkReady(intr, ipAddr, gateway, underlayGateway, verbose, 1); err != nil {
			err = nil
		}
	} else {
		err = waitNetworkReady(intr, ipAddr, gateway, underlayGateway, verbose, gatewayCheckMaxRetry)
	}
	return err
}

func setupVethPair(containerID, ifName string, mtu int) (string, string, error) {
	var err error
	hostNicName, containerNicName := generateNicName(containerID, ifName)
	// Create a veth pair, put one end to container ,the other to ovs port
	// NOTE: DO NOT use ovs internal type interface for container.
	// Kubernetes will detect 'eth0' nic in pod, so the nic name in pod must be 'eth0'.
	// When renaming internal interface to 'eth0', ovs will delete and recreate this interface.
	veth := netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: hostNicName}, PeerName: containerNicName}
	if mtu > 0 {
		veth.MTU = mtu
	}
	if err = netlink.LinkAdd(&veth); err != nil {
		if err := netlink.LinkDel(&veth); err != nil {
			klog.Errorf("failed to delete veth %v", err)
			return "", "", err
		}
		return "", "", fmt.Errorf("failed to create veth for %v", err)
	}
	return hostNicName, containerNicName, nil
}

func generateNicName(containerID, ifname string) (string, string) {
	if ifname == "eth0" {
		return fmt.Sprintf("%s_h", containerID[0:12]), fmt.Sprintf("%s_c", containerID[0:12])
	}
	// The nic name is 14 length and have prefix pod in the Kubevirt v1.0.0
	if strings.HasPrefix(ifname, "pod") && len(ifname) == 14 {
		ifname = ifname[3 : len(ifname)-4]
		return fmt.Sprintf("%s_%s_h", containerID[0:12-len(ifname)], ifname), fmt.Sprintf("%s_%s_c", containerID[0:12-len(ifname)], ifname)
	}
	return fmt.Sprintf("%s_%s_h", containerID[0:12-len(ifname)], ifname), fmt.Sprintf("%s_%s_c", containerID[0:12-len(ifname)], ifname)
}

func configureHostNic(nicName string) error {
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

func (csh cniServerHandler) handleDel(req *restful.Request, resp *restful.Response) {
	var podRequest request.CniRequest
	if err := req.ReadEntity(&podRequest); err != nil {
		errMsg := fmt.Errorf("parse del request failed %v", err)
		klog.Error(errMsg)
		if err := resp.WriteHeaderAndEntity(http.StatusBadRequest, request.CniResponse{Err: errMsg.Error()}); err != nil {
			klog.Errorf("failed to write response, %v", err)
		}
		return
	}

	pod, err := csh.Controller.podsLister.Pods(podRequest.PodNamespace).Get(podRequest.PodName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			resp.WriteHeader(http.StatusNoContent)
			return
		}

		errMsg := fmt.Errorf("parse del request failed %v", err)
		klog.Error(errMsg)
		if err := resp.WriteHeaderAndEntity(http.StatusBadRequest, request.CniResponse{Err: errMsg.Error()}); err != nil {
			klog.Errorf("failed to write response, %v", err)
		}
		return
	}

	klog.Infof("del port request: %+v", podRequest)
	//if err := csh.validatePodRequest(&podRequest); err != nil {
	//	klog.Error(err)
	//	if err := resp.WriteHeaderAndEntity(http.StatusBadRequest, request.CniResponse{Err: err.Error()}); err != nil {
	//		klog.Errorf("failed to write response, %v", err)
	//	}
	//	return
	//}

	if pod.Annotations != nil && (podRequest.Provider == consts.OvnProviderName || podRequest.CniType == consts.CniVendorName) {

		var nicType string
		switch {
		case podRequest.DeviceID != "":
			nicType = consts.PortTypeOffload
		case podRequest.VhostUserSocketVolumeName != "":
			nicType = consts.PortTypeDpdk
			//if err = removeShortSharedDir(pod, podRequest.VhostUserSocketVolumeName); err != nil {
			//	klog.Error(err.Error())
			//	if err = resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: err.Error()}); err != nil {
			//		klog.Errorf("failed to write response: %v", err)
			//	}
			//	return
			//}
		default:
			nicType = pod.Annotations[fmt.Sprintf(consts.AnnotationPodNicTypeTemplate, podRequest.Provider)]
		}
		//vmName := pod.Annotations[fmt.Sprintf(util.VMAnnotationTemplate, podRequest.Provider)]
		//if vmName != "" {
		//	podRequest.PodName = vmName
		//}

		err = csh.deleteNic(podRequest.PodName, podRequest.PodNamespace, podRequest.ContainerID, podRequest.NetNs, podRequest.DeviceID, podRequest.IfName, nicType)
		if err != nil {
			errMsg := fmt.Errorf("del nic failed %v", err)
			klog.Error(errMsg)
			if err := resp.WriteHeaderAndEntity(http.StatusInternalServerError, request.CniResponse{Err: errMsg.Error()}); err != nil {
				klog.Errorf("failed to write response, %v", err)
			}
			return
		}
	}

	resp.WriteHeader(http.StatusNoContent)
}

func (csh cniServerHandler) deleteNic(podName, podNamespace, containerID, _, deviceID, ifName, nicType string) error {
	var nicName string
	hostNicName, containerNicName := generateNicName(containerID, ifName)

	if nicType == consts.PortTypeInternal {
		nicName = containerNicName
	} else {
		nicName = hostNicName
	}

	// Remove ovs port
	//output, err := ovs.Exec(ovs.IfExists, "--with-iface", "del-port", "br-int", nicName)
	err := csh.Config.vSwitchClient.DeletePort("br-int", nicName)
	if err != nil {
		return fmt.Errorf("failed to delete ovs port %v: %b", nicName, err)
	}

	//if err = ovs.ClearPodBandwidth(podName, podNamespace, ""); err != nil {
	//	return err
	//}
	//if err = ovs.ClearHtbQosQueue(podName, podNamespace, ""); err != nil {
	//	return err
	//}

	if deviceID == "" {
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
