package main

import (
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"strings"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"

	"github.com/fengjinlin/kube-ovn/pkg/cni/netconf"
	"github.com/fengjinlin/kube-ovn/pkg/cni/request"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
	"github.com/fengjinlin/kube-ovn/versions"
)

func main() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
	runtime.LockOSThread()

	about := fmt.Sprintf("CNI kube-ovs plugin %s", versions.VERSION)
	skel.PluginMainFuncs(skel.CNIFuncs{
		Add:    cmdAdd,
		Del:    cmdDel,
		Check:  nil,
		GC:     nil,
		Status: nil,
	}, version.All, about)
}

func cmdAdd(args *skel.CmdArgs) error {
	netConf, cniVersion, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	podName, err := parseValueFromArgs("K8S_POD_NAME", args.Args)
	if err != nil {
		return err
	}
	podNamespace, err := parseValueFromArgs("K8S_POD_NAMESPACE", args.Args)
	if err != nil {
		return err
	}
	if netConf.Provider == "" && netConf.Type == consts.CniVendorName && args.IfName == "eth0" {
		netConf.Provider = consts.OvnProviderName
	}

	client := request.NewCniServerClient(netConf.ServerSocket)
	response, err := client.Add(request.CniRequest{
		CniType:                   netConf.Type,
		PodName:                   podName,
		PodNamespace:              podNamespace,
		ContainerID:               args.ContainerID,
		NetNs:                     args.Netns,
		IfName:                    args.IfName,
		Provider:                  netConf.Provider,
		Routes:                    netConf.Routes,
		DNS:                       netConf.DNS,
		DeviceID:                  netConf.DeviceID,
		VfDriver:                  netConf.VfDriver,
		VhostUserSocketVolumeName: netConf.VhostUserSocketVolumeName,
		VhostUserSocketName:       netConf.VhostUserSocketName,
	})

	if err != nil {
		return types.NewError(types.ErrTryAgainLater, "RPC failed", err.Error())
	}

	result := generateCNIResult(response, args.Netns)
	return types.PrintResult(&result, cniVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	netConf, _, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	client := request.NewCniServerClient(netConf.ServerSocket)
	podName, err := parseValueFromArgs("K8S_POD_NAME", args.Args)
	if err != nil {
		return err
	}
	podNamespace, err := parseValueFromArgs("K8S_POD_NAMESPACE", args.Args)
	if err != nil {
		return err
	}
	if netConf.Type == consts.CniVendorName && args.IfName == "eth0" {
		netConf.Provider = consts.OvnProviderName
	}

	err = client.Del(request.CniRequest{
		CniType:                   netConf.Type,
		PodName:                   podName,
		PodNamespace:              podNamespace,
		ContainerID:               args.ContainerID,
		NetNs:                     args.Netns,
		IfName:                    args.IfName,
		Provider:                  netConf.Provider,
		DeviceID:                  netConf.DeviceID,
		VhostUserSocketVolumeName: netConf.VhostUserSocketVolumeName,
	})
	if err != nil {
		return types.NewError(types.ErrTryAgainLater, "RPC failed", err.Error())
	}
	return nil
}

func loadNetConf(bytes []byte) (*netconf.NetConf, string, error) {
	n := &netconf.NetConf{}
	if err := json.Unmarshal(bytes, n); err != nil {
		return nil, "", types.NewError(types.ErrDecodingFailure, "failed to load netconf", err.Error())
	}

	if n.Type != consts.CniVendorName && n.IPAM != nil {
		n.Provider = n.IPAM.Provider
		n.ServerSocket = n.IPAM.ServerSocket
		//n.Routes = n.IPAM.Routes
	}

	if n.ServerSocket == "" {
		return nil, "", types.NewError(types.ErrInvalidNetworkConfig, "Invalid Configuration", fmt.Sprintf("server_socket is required in cni.conf, %+v", n))
	}

	if n.Provider == "" {
		n.Provider = consts.OvnProviderName
	}

	return n, n.CNIVersion, nil
}

func parseValueFromArgs(key, argString string) (string, error) {
	if argString == "" {
		return "", types.NewError(types.ErrInvalidNetworkConfig, "Invalid Configuration", "CNI_ARGS is required")
	}
	args := strings.Split(argString, ";")
	for _, arg := range args {
		if strings.HasPrefix(arg, fmt.Sprintf("%s=", key)) {
			value := strings.TrimPrefix(arg, fmt.Sprintf("%s=", key))
			if len(value) > 0 {
				return value, nil
			}
		}
	}
	return "", types.NewError(types.ErrInvalidNetworkConfig, "Invalid Configuration", fmt.Sprintf("%s is required in CNI_ARGS", key))
}

func generateCNIResult(cniResponse *request.CniResponse, netns string) current.Result {
	result := current.Result{
		CNIVersion: current.ImplementedSpecVersion,
		DNS:        cniResponse.DNS,
		Routes:     parseRoutes(cniResponse.Routes),
	}
	_, mask, _ := net.ParseCIDR(cniResponse.CIDR)
	podIface := current.Interface{
		Name:    cniResponse.PodNicName,
		Mac:     cniResponse.MacAddress,
		Sandbox: netns,
	}
	switch cniResponse.Protocol {
	case utils.ProtocolIPv4:
		ip, route := assignV4Address(cniResponse.IPAddress, cniResponse.Gateway, mask)
		result.IPs = []*current.IPConfig{ip}
		if len(result.Routes) == 0 && route != nil {
			result.Routes = []*types.Route{route}
		}
		result.Interfaces = []*current.Interface{&podIface}
	case utils.ProtocolIPv6:
		ip, route := assignV6Address(cniResponse.IPAddress, cniResponse.Gateway, mask)
		result.IPs = []*current.IPConfig{ip}
		if len(result.Routes) == 0 && route != nil {
			result.Routes = []*types.Route{route}
		}
		result.Interfaces = []*current.Interface{&podIface}
	case utils.ProtocolDual:
		var netMask *net.IPNet
		var gwStr string
		addRoutes := len(result.Routes) == 0
		for _, cidrBlock := range strings.Split(cniResponse.CIDR, ",") {
			_, netMask, _ = net.ParseCIDR(cidrBlock)
			gwStr = ""
			if utils.CheckProtocol(cidrBlock) == utils.ProtocolIPv4 {
				ipStr := strings.Split(cniResponse.IPAddress, ",")[0]
				if cniResponse.Gateway != "" {
					gwStr = strings.Split(cniResponse.Gateway, ",")[0]
				}

				ip, route := assignV4Address(ipStr, gwStr, netMask)
				result.IPs = append(result.IPs, ip)
				if addRoutes && route != nil {
					result.Routes = append(result.Routes, route)
				}
			} else if utils.CheckProtocol(cidrBlock) == utils.ProtocolIPv6 {
				ipStr := strings.Split(cniResponse.IPAddress, ",")[1]
				if cniResponse.Gateway != "" {
					gwStr = strings.Split(cniResponse.Gateway, ",")[1]
				}

				ip, route := assignV6Address(ipStr, gwStr, netMask)
				result.IPs = append(result.IPs, ip)
				if addRoutes && route != nil {
					result.Routes = append(result.Routes, route)
				}
			}
		}
		result.Interfaces = []*current.Interface{&podIface}
	}

	return result
}

func parseRoutes(routes []request.Route) []*types.Route {
	parsedRoutes := make([]*types.Route, len(routes))
	for i, r := range routes {
		if r.Destination == "" {
			if utils.CheckProtocol(r.Gateway) == utils.ProtocolIPv4 {
				r.Destination = "0.0.0.0/0"
			} else {
				r.Destination = "::/0"
			}
		}
		parsedRoutes[i] = &types.Route{GW: net.ParseIP(r.Gateway)}
		if _, cidr, err := net.ParseCIDR(r.Destination); err == nil {
			parsedRoutes[i].Dst = *cidr
		}
	}
	return parsedRoutes
}

func assignV4Address(ipAddress, gateway string, mask *net.IPNet) (*current.IPConfig, *types.Route) {
	ip := &current.IPConfig{
		Address:   net.IPNet{IP: net.ParseIP(ipAddress).To4(), Mask: mask.Mask},
		Gateway:   net.ParseIP(gateway).To4(),
		Interface: current.Int(0),
	}

	var route *types.Route
	if gw := net.ParseIP(gateway); gw != nil {
		route = &types.Route{
			Dst: net.IPNet{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, 32)},
			GW:  net.ParseIP(gateway).To4(),
		}
	}

	return ip, route
}

func assignV6Address(ipAddress, gateway string, mask *net.IPNet) (*current.IPConfig, *types.Route) {
	ip := &current.IPConfig{
		Address:   net.IPNet{IP: net.ParseIP(ipAddress).To16(), Mask: mask.Mask},
		Gateway:   net.ParseIP(gateway).To16(),
		Interface: current.Int(0),
	}

	var route *types.Route
	if gw := net.ParseIP(gateway); gw != nil {
		route = &types.Route{
			Dst: net.IPNet{IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)},
			GW:  net.ParseIP(gateway).To16(),
		}
	}

	return ip, route
}
