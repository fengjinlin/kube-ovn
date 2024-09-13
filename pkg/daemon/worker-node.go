package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	coreinformer "k8s.io/client-go/informers/core/v1"
	corelister "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	"github.com/containernetworking/plugins/pkg/utils/sysctl"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type NodeWorker interface {
	InitNodeChassis() error
	InitNodeGateway() error
	InitForOS() error
}

func NewNodeWorker(config *Configuration,
	nodeInformer coreinformer.NodeInformer) (NodeWorker, error) {
	return &nodeWorker{
		config:       config,
		nodeInformer: nodeInformer,
		nodeLister:   nodeInformer.Lister(),
	}, nil
}

type nodeWorker struct {
	config *Configuration

	nodeInformer coreinformer.NodeInformer
	nodeLister   corelister.NodeLister
}

func (w *nodeWorker) InitNodeChassis() error {
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

	nodeName := w.config.NodeName
	node, err := w.nodeLister.Get(nodeName)
	if err != nil {
		klog.Errorf("failed to get node %s: %v", nodeName, err)
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
	_, err = w.config.KubeClient.CoreV1().Nodes().Patch(context.Background(), nodeName, types.JSONPatchType, []byte(patchPayload), metav1.PatchOptions{}, "")
	if err != nil {
		klog.Errorf("failed to patch node %s: %v", node, err)
		return err
	}
	klog.Infof("finish adding chassis annotation")
	return nil
}

func (w *nodeWorker) InitNodeGateway() error {
	var portName, ip, cidr, macAddr, gw string
	for {
		nodeName := w.config.NodeName
		node, err := w.config.KubeClient.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
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

	//ipAddr = utils.GetIPAddrWithMask(ip, cidr)

	port, err := w.config.vSwitchClient.GetPort(portName, true)
	if err != nil {
		klog.Errorf("failed to get port %s %v", portName, err)
		return err
	}

	if port == nil {
		externalIds := map[string]string{
			"iface-id": portName,
			"ip":       ip,
		}
		err = w.config.vSwitchClient.CreatePort(consts.DefaultBridgeName, portName, consts.NodeNicName, consts.IfaceTypeInternal, externalIds)
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
	if err = configureNic(consts.NodeNicName, ipAddr, macAddr, w.config.MTU, false); err != nil {
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

func (w *nodeWorker) InitForOS() error {
	// disable checksum for genev_sys_6081 as default
	cmd := exec.Command("sh", "-c", "ethtool -K genev_sys_6081 tx off")
	if err := cmd.Run(); err != nil {
		err := fmt.Errorf("failed to set checksum off for genev_sys_6081, %v", err)
		// should not affect cni pod running if failed, just record err log
		klog.Error(err)
	}
	return nil
}
