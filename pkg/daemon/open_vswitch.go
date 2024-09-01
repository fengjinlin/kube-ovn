package daemon

import (
	"fmt"
	"os"
	"os/exec"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"

	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

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

func setEncapIP(ip string) error {
	raw, err := exec.Command(
		"ovs-vsctl", "set", "open", ".", fmt.Sprintf("external-ids:ovn-encap-ip=%s", ip)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set ovn-encap-ip, %s", string(raw))
	}
	return nil
}

func disableChecksum() error {
	raw, err := exec.Command(
		"ovs-vsctl", "set", "open", ".", "external-ids:ovn-encap-csum=false").CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to set ovn-encap-csum, %s", string(raw))
	}
	return nil
}
