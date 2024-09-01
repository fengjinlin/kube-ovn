package daemon

import (
	"context"
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/vswitch"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"os/exec"
)

func InitOvsBridges(config *Configuration) error {
	bridges, err := config.vSwitchClient.ListBridges(func(br *vswitch.Bridge) bool {
		if br.ExternalIDs != nil && br.ExternalIDs[consts.ExternalIDsKeyVendor] == consts.CniVendorName {
			return true
		}
		return false
	})
	if err != nil {
		klog.Error(err)
		return err
	}
	for _, bridge := range bridges {
		if err := utils.SetLinkUp(bridge.Name); err != nil {
			klog.Error(err)
			return err
		}

	}

	return initNicConfig(config)
}

func initNicConfig(config *Configuration) error {
	node, err := config.KubeClient.CoreV1().Nodes().Get(context.Background(), config.NodeName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("Failed to find node info, err: %v", err)
		return err
	}

	var mtu int
	encapIP := getEncapIP(node)
	if config.Iface, mtu, err = getIfaceByIP(encapIP); err != nil {
		klog.Errorf("failed to get interface by IP %s: %v", encapIP, err)
		return err
	}

	encapIsIPv6 := utils.CheckProtocol(encapIP) == utils.ProtocolIPv6

	if config.MTU == 0 {
		switch config.NetworkType {
		case consts.NetworkTypeGeneve, consts.NetworkTypeVlan:
			config.MTU = mtu - consts.GeneveHeaderLength
		case consts.NetworkTypeVxlan:
			config.MTU = mtu - consts.VxlanHeaderLength
		//case util.NetworkTypeStt:
		//	config.MTU = mtu - util.SttHeaderLength
		default:
			return fmt.Errorf("invalid network type: %s", config.NetworkType)
		}
		if encapIsIPv6 {
			// IPv6 header size is 40
			config.MTU -= 20
		}
	}

	config.MSS = config.MTU - consts.TCPIPHeaderLength

	if !config.EncapChecksum {
		if err := disableChecksum(); err != nil {
			klog.Errorf("failed to set checksum offload, %v", err)
		}
	}

	return setEncapIP(encapIP)
}

func InitForOS() error {
	// disable checksum for genev_sys_6081 as default
	cmd := exec.Command("sh", "-c", "ethtool -K genev_sys_6081 tx off")
	if err := cmd.Run(); err != nil {
		err := fmt.Errorf("failed to set checksum off for genev_sys_6081, %v", err)
		// should not affect cni pod running if failed, just record err log
		klog.Error(err)
	}
	return nil
}
