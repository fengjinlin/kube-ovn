package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"strings"
	"time"

	"github.com/containernetworking/plugins/pkg/utils/sysctl"
	goping "github.com/prometheus-community/pro-bing"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"

	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

func InitNodeChassis(config *Configuration) error {
	chassisId, err := os.ReadFile(consts.ChassisIdLocation)
	if err != nil {
		klog.Errorf("read chassis file failed, %v", err)
		return err
	}

	chassesName := strings.TrimSpace(string(chassisId))
	if chassesName == "" {
		// not ready yet
		err = fmt.Errorf("chassis id is empty")
		klog.Error(err)
		return err
	}

	hostname := config.NodeName
	node, err := config.KubeClient.CoreV1().Nodes().Get(context.Background(), hostname, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("failed to get node %s %v", hostname, err)
		return err
	}

	if annoChassesName, ok := node.Annotations[consts.AnnotationChassis]; ok {
		if annoChassesName == chassesName {
			return nil
		}
		klog.Infof("chassis id changed, old: %s, new: %s", annoChassesName, chassesName)
	}

	node.Annotations[consts.AnnotationChassis] = chassesName
	patchPayloadTemplate := `[{
        "op": "%s",
        "path": "/metadata/annotations",
        "value": %s
    }]`
	op := "add"
	raw, _ := json.Marshal(node.Annotations)
	patchPayload := fmt.Sprintf(patchPayloadTemplate, op, raw)
	_, err = config.KubeClient.CoreV1().Nodes().Patch(context.Background(), hostname, types.JSONPatchType, []byte(patchPayload), metav1.PatchOptions{}, "")
	if err != nil {
		klog.Errorf("patch node %s failed %v", hostname, err)
		return err
	}
	klog.Infof("finish adding chassis annotation")
	return nil
}

func InitNodeGateway(config *Configuration) error {
	var portName, ip, cidr, macAddr, gw string
	for {
		nodeName := config.NodeName
		node, err := config.KubeClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		if err != nil {
			klog.Errorf("failed to get node %s info %v", nodeName, err)
			return err
		}
		if node.Annotations[consts.AnnotationJoinIP] == "" {
			klog.Warningf("no ovn0 address for node %s, please check kube-ovn-controller logs", nodeName)
			time.Sleep(3 * time.Second)
			continue
		}
		//if err := utils.ValidatePodNetwork(node.Annotations); err != nil {
		//	klog.Errorf("validate node %s address annotation failed, %v", nodeName, err)
		//	time.Sleep(3 * time.Second)
		//	continue
		//}
		macAddr = node.Annotations[consts.AnnotationJoinMac]
		ip = node.Annotations[consts.AnnotationJoinIP]
		cidr = node.Annotations[consts.AnnotationJoinCidr]
		portName = node.Annotations[consts.AnnotationJoinLogicalSwitchPort]
		gw = node.Annotations[consts.AnnotationJoinGateway]
		break
	}

	klog.Infof("node join info: %s %s %s %s %s", ip, cidr, gw, cidr, portName)

	mac, err := net.ParseMAC(macAddr)
	if err != nil {
		return fmt.Errorf("failed to parse mac %s %v", mac, err)
	}

	//ipAddr = utils.GetIPAddrWithMask(ip, cidr)

	port, err := config.vSwitchClient.GetPort(portName, true)
	if err != nil {
		klog.Errorf("failed to get port %s %v", portName, err)
		return err
	}

	if port == nil {
		externalIds := map[string]string{
			"iface-id": portName,
			"ip":       ip,
		}
		err = config.vSwitchClient.CreatePort(consts.DefaultBridgeName, portName, consts.NodeNicName, consts.IfaceTypeInternal, externalIds)
		if err != nil {
			klog.Errorf("failed to create port %s %v", portName, err)
			return err
		}
	}

	value, err := sysctl.Sysctl(fmt.Sprintf("net.ipv6.conf.%s.addr_gen_mode", consts.NodeNicName))
	if err == nil {
		if value != "0" {
			if _, err = sysctl.Sysctl(fmt.Sprintf("net.ipv6.conf.%s.addr_gen_mode", consts.NodeNicName), "0"); err != nil {
				return fmt.Errorf("failed to set ovn0 addr_gen_mode: %v", err)
			}
		}
	}

	ipAddr := utils.GetIPAddrWithMask(ip, cidr)
	if err = configureNic(config, consts.NodeNicName, ipAddr, mac, config.MTU, false); err != nil {
		return err
	}

	// ping ovn0 gw to activate the flow
	klog.Infof("wait ovn0 gw ready")
	if err := waitNetworkReady(consts.NodeNicName, ip, gw, false, true, 200); err != nil {
		klog.Errorf("failed to init ovn0 check: %v", err)
		return err
	}
	return nil
}

func waitNetworkReady(nic, ipAddr, gateway string, underlayGateway, verbose bool, maxRetry int) error {
	ips := strings.Split(ipAddr, ",")
	for i, gw := range strings.Split(gateway, ",") {
		src := strings.Split(ips[i], "/")[0]
		if underlayGateway && utils.CheckProtocol(gw) == utils.ProtocolIPv4 {
			mac, count, err := utils.ArpResolve(nic, src, gw, time.Second, maxRetry)
			//cniConnectivityResult.WithLabelValues(nodeName).Add(float64(count))
			if err != nil {
				err = fmt.Errorf("network %s with gateway %s is not ready for interface %s after %d checks: %v", ips[i], gw, nic, count, err)
				klog.Warning(err)
				return err
			}
			if verbose {
				klog.Infof("MAC addresses of gateway %s is %s", gw, mac.String())
				klog.Infof("network %s with gateway %s is ready for interface %s after %d checks", ips[i], gw, nic, count)
			}
		} else {
			_, err := pingGateway(gw, src, verbose, maxRetry)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func pingGateway(gw, src string, verbose bool, maxRetry int) (count int, err error) {
	pinger, err := goping.NewPinger(gw)
	if err != nil {
		return 0, fmt.Errorf("failed to init pinger: %v", err)
	}
	pinger.SetPrivileged(true)
	// CNITimeoutSec = 220, cannot exceed
	pinger.Count = maxRetry
	pinger.Timeout = time.Duration(maxRetry) * time.Second
	pinger.Interval = time.Second

	pinger.OnRecv = func(_ *goping.Packet) {
		pinger.Stop()
	}

	pinger.OnSend = func(_ *goping.Packet) {
		if pinger.PacketsRecv == 0 && pinger.PacketsSent != 0 && pinger.PacketsSent%3 == 0 {
			klog.Warningf("%s network not ready after %d ping to gateway %s", src, pinger.PacketsSent, gw)
		}
	}

	if err = pinger.Run(); err != nil {
		klog.Errorf("failed to run pinger for destination %s: %v", gw, err)
		return 0, err
	}

	if pinger.PacketsRecv == 0 {
		klog.Warningf("%s network not ready after %d ping, gw %s", src, pinger.PacketsSent, gw)
		return pinger.PacketsSent, fmt.Errorf("no packets received from gateway %s", gw)
	}

	//cniConnectivityResult.WithLabelValues(nodeName).Add(float64(pinger.PacketsSent))
	if verbose {
		klog.Infof("%s network ready after %d ping, gw %s", src, pinger.PacketsSent, gw)
	}

	return pinger.PacketsSent, nil
}

func configureNic(config *Configuration, link, ip string, macAddr net.HardwareAddr, mtu int, detectIPConflict bool) error {
	nodeLink, err := netlink.LinkByName(link)
	if err != nil {
		return fmt.Errorf("can not find nic %s: %v", link, err)
	}

	if err = netlink.LinkSetHardwareAddr(nodeLink, macAddr); err != nil {
		return fmt.Errorf("can not set mac address to nic %s: %v", link, err)
	}

	if mtu > 0 {
		if nodeLink.Type() == "openvswitch" {
			//_, err = ovs.Exec("set", "interface", link, fmt.Sprintf(`mtu_request=%d`, mtu))

		} else {
			err = netlink.LinkSetMTU(nodeLink, mtu)
		}
		if err != nil {
			return fmt.Errorf("failed to set nic %s mtu: %v", link, err)
		}
	}

	if nodeLink.Attrs().OperState != netlink.OperUp {
		if err = netlink.LinkSetUp(nodeLink); err != nil {
			return fmt.Errorf("failed to set node nic %s up: %v", link, err)
		}
	}

	ipDelMap := make(map[string]netlink.Addr)
	ipAddMap := make(map[string]netlink.Addr)
	ipAddrs, err := netlink.AddrList(nodeLink, unix.AF_UNSPEC)
	if err != nil {
		return fmt.Errorf("failed to get addr %s: %v", nodeLink, err)
	}
	for _, ipAddr := range ipAddrs {
		if ipAddr.IP.IsLinkLocalUnicast() {
			// skip 169.254.0.0/16 and fe80::/10
			continue
		}
		ipDelMap[ipAddr.IPNet.String()] = ipAddr
	}

	for _, ipStr := range strings.Split(ip, ",") {
		// Do not reassign same address for link
		if _, ok := ipDelMap[ipStr]; ok {
			delete(ipDelMap, ipStr)
			continue
		}

		ipAddr, err := netlink.ParseAddr(ipStr)
		if err != nil {
			return fmt.Errorf("failed to parse address %s: %v", ipStr, err)
		}
		ipAddMap[ipStr] = *ipAddr
	}

	for ip, addr := range ipDelMap {
		klog.Infof("delete ip address %s on %s", ip, link)
		if err = netlink.AddrDel(nodeLink, &addr); err != nil {
			return fmt.Errorf("delete address %s: %v", addr, err)
		}
	}
	for ip, addr := range ipAddMap {
		if detectIPConflict && addr.IP.To4() != nil {
			ip := addr.IP.String()
			mac, err := utils.ArpDetectIPConflict(link, ip, macAddr)
			if err != nil {
				err = fmt.Errorf("failed to detect address conflict for %s on link %s: %v", ip, link, err)
				klog.Error(err)
				return err
			}
			if mac != nil {
				return fmt.Errorf("IP address %s has already been used by host with MAC %s", ip, mac)
			}
		}
		if addr.IP.To4() != nil && !detectIPConflict {
			// when detectIPConflict is true, free arp is already broadcast in the step of announcement
			if err := utils.AnnounceArpAddress(link, addr.IP.String(), macAddr, 1, 1*time.Second); err != nil {
				klog.Warningf("failed to broadcast free arp with err %v ", err)
			}
		}

		klog.Infof("add ip address %s to %s", ip, link)
		if err = netlink.AddrAdd(nodeLink, &addr); err != nil {
			return fmt.Errorf("can not add address %v to nic %s: %v", addr, link, err)
		}
	}

	if err = netlink.LinkSetTxQLen(nodeLink, 1000); err != nil {
		return fmt.Errorf("can not set host nic %s qlen: %v", link, err)
	}

	return nil
}
