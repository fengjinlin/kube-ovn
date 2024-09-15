package daemon

import (
	"fmt"
	"k8s.io/apimachinery/pkg/util/wait"
	"os"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	coreinformer "k8s.io/client-go/informers/core/v1"
	corelister "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/vswitch"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type OvsWorker interface {
	InitOvs() error

	Run(stopCh <-chan struct{})
}

func NewOvsWorker(config *Configuration,
	nodeInformer coreinformer.NodeInformer) (OvsWorker, error) {

	return &ovsWorker{
		config:     config,
		nodeLister: nodeInformer.Lister(),
	}, nil
}

type ovsWorker struct {
	config *Configuration

	nodeLister corelister.NodeLister
}

func (w *ovsWorker) Run(stopCh <-chan struct{}) {
	go wait.Until(w.cleanLostInterface, time.Minute, stopCh)

	<-stopCh
	klog.Info("stopping ovs worker")
}

// cleanLostInterface will clean up related ovs port, interface and qos
// When reboot node, the ovs internal interface will be deleted.
func (w *ovsWorker) cleanLostInterface() {
	klog.Info("cleaning lost ovs interface")
	lostIfaceFilter := func(iface *vswitch.Interface) bool {
		return iface.Ofport != nil && *iface.Ofport == -1 &&
			len(iface.ExternalIDs) > 0 && iface.ExternalIDs[consts.ExternalIDsKeyPodNS] != "" &&
			iface.Error != nil && strings.Contains(*iface.Error, "No such device")
	}
	lostIfaceList, err := w.config.vSwitchClient.ListInterfaceByFilter(lostIfaceFilter)
	if err != nil {
		klog.Errorf("list lost interface error: %v", err)
		return
	}
	klog.Infof("cleaning lost ovs interface, found %d", len(lostIfaceList))
	for _, iface := range lostIfaceList {
		if err = w.config.vSwitchClient.DeletePort(consts.DefaultBridgeName, iface.Name); err != nil {
			klog.Errorf("failed to delete lost port %s: %v", iface.Name, err)
		}
	}
}

func (w *ovsWorker) InitOvs() error {
	// init bridges
	{
		bridges, err := w.config.vSwitchClient.ListBridges(func(br *vswitch.Bridge) bool {
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
	}

	// init encap
	{
		//node, err := config.KubeClient.CoreV1().Nodes().Get(context.Background(), config.NodeName, metav1.GetOptions{})
		node, err := w.nodeLister.Get(w.config.NodeName)
		if err != nil {
			klog.Errorf("Failed to find node info, err: %v", err)
			return err
		}

		var mtu int
		encapIP := getEncapIP(node)
		if w.config.Iface, mtu, err = getIfaceByIP(encapIP); err != nil {
			klog.Errorf("failed to get interface by IP %s: %v", encapIP, err)
			return err
		}

		encapIsIPv6 := utils.CheckProtocol(encapIP) == utils.ProtocolIPv6

		if w.config.MTU == 0 {
			switch w.config.NetworkType {
			case consts.NetworkTypeGeneve, consts.NetworkTypeVlan:
				w.config.MTU = mtu - consts.GeneveHeaderLength
			case consts.NetworkTypeVxlan:
				w.config.MTU = mtu - consts.VxlanHeaderLength
			//case util.NetworkTypeStt:
			//	config.MTU = mtu - util.SttHeaderLength
			default:
				return fmt.Errorf("invalid network type: %s", w.config.NetworkType)
			}
			if encapIsIPv6 {
				// IPv6 header size is 40
				w.config.MTU -= 20
			}
		}

		w.config.MSS = w.config.MTU - consts.TCPIPHeaderLength

		ovsList, err := w.config.vSwitchClient.ListOpenVSwitch()
		if err != nil {
			return fmt.Errorf("failed to list open vswitch: %v", err)
		}

		var (
			ovs     = ovsList[0]
			changed = false
		)

		if ovs.ExternalIDs == nil {
			ovs.ExternalIDs = make(map[string]string)
		}

		if ovs.ExternalIDs["ovn-encap-ip"] != encapIP {
			ovs.ExternalIDs["ovn-encap-ip"] = encapIP
			changed = true
		}

		if !w.config.EncapChecksum {
			if ovs.ExternalIDs["ovn-encap-csum"] != "false" {
				ovs.ExternalIDs["ovn-encap-csum"] = "false"
				changed = true
			}
		} else {
			if ovs.ExternalIDs["ovn-encap-csum"] != "true" {
				ovs.ExternalIDs["ovn-encap-csum"] = "true"
				changed = true
			}
		}

		if changed {
			if err = w.config.vSwitchClient.UpdateOpenVSwitch(ovs, &ovs.ExternalIDs); err != nil {
				return fmt.Errorf("failed to update openvswitch: %v", err)
			}
		}
	}

	return nil
}

func getEncapIP(node *corev1.Node) string {
	if podIP := os.Getenv(consts.PodIP); podIP != "" {
		return podIP
	}

	klog.Info("environment variable POD_IP not found, fall back to node address")
	ipv4, ipv6 := utils.GetNodeInternalIP(node)
	if ipv4 != "" {
		return ipv4
	}
	return ipv6
}
