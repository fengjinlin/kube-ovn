package netconf

import (
	"github.com/containernetworking/cni/pkg/types"

	"github.com/fengjinlin/kube-ovn/pkg/cni/request"
)

type NetConf struct {
	types.NetConf

	ServerSocket string          `json:"server_socket"`
	Provider     string          `json:"provider"`
	Routes       []request.Route `json:"routes"`
	IPAM         *IPAMConf       `json:"ipam"`
	// PciAddrs in case of using sriov
	DeviceID string `json:"deviceID"`
	VfDriver string `json:"vf_driver"`
	// for dpdk
	VhostUserSocketVolumeName string `json:"vhost_user_socket_volume_name"`
	VhostUserSocketName       string `json:"vhost_user_socket_name"`
}

type IPAMConf struct {
	Type         string `json:"type"`
	ServerSocket string `json:"server_socket"`
	Provider     string `json:"provider"`
	//Routes       []request.Route `json:"routes"`
}
