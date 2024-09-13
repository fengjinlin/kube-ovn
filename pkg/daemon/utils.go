package daemon

import (
	"fmt"
	"net"
	"strings"
	"time"

	goping "github.com/prometheus-community/pro-bing"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
	"k8s.io/klog/v2"

	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

func configureNic(link, ip, mac string, mtu int, detectIPConflict bool) error {
	macAddr, err := net.ParseMAC(mac)
	if err != nil {
		return fmt.Errorf("failed to parse mac %s %v", macAddr, err)
	}

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

func checkGatewayReady(gwCheckMode int, intr, ipAddr, gateway string, underlayGateway, verbose bool) error {
	var err error

	if gwCheckMode == consts.GatewayCheckModeArpNotConcerned || gwCheckMode == consts.GatewayCheckModePingNotConcerned {
		// ignore error if disableGatewayCheck=true
		if err = waitNetworkReady(intr, ipAddr, gateway, underlayGateway, verbose, 1); err != nil {
			err = nil
		}
	} else {
		err = waitNetworkReady(intr, ipAddr, gateway, underlayGateway, verbose, consts.GatewayCheckMaxRetry)
	}
	return err
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

func getIfaceByIP(ip string) (string, int, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return "", 0, err
	}

	for _, link := range links {
		addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
		if err != nil {
			return "", 0, fmt.Errorf("failed to get addresses of link %s: %v", link.Attrs().Name, err)
		}
		for _, addr := range addrs {
			if addr.IPNet.Contains(net.ParseIP(ip)) && addr.IP.String() == ip {
				return link.Attrs().Name, link.Attrs().MTU, nil
			}
		}
	}

	return "", 0, fmt.Errorf("failed to find interface by address %s", ip)
}

func getSrcIPsByRoutes(iface *net.Interface) ([]string, error) {
	link, err := netlink.LinkByName(iface.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get link %s: %v", iface.Name, err)
	}
	routes, err := netlink.RouteList(link, netlink.FAMILY_ALL)
	if err != nil {
		return nil, fmt.Errorf("failed to get routes on link %s: %v", iface.Name, err)
	}

	srcIPs := make([]string, 0, 2)
	for _, r := range routes {
		if r.Src != nil && r.Scope == netlink.SCOPE_LINK {
			srcIPs = append(srcIPs, r.Src.String())
		}
	}
	return srcIPs, nil
}
