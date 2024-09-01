package utils

import (
	"crypto/tls"
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	"net"
	"net/url"
	"strings"
	"time"
)

func DialAPIServer(host string) error {
	interval := 3 * time.Second
	timer := time.NewTimer(interval)
	for i := 0; i < 10; i++ {
		err := DialTCP(host, interval, true)
		if err == nil {
			return nil
		}
		klog.Warningf("failed to dial apiserver %q: %v", host, err)
		<-timer.C
		timer.Reset(interval)
	}

	return fmt.Errorf("timed out dialing apiserver %q", host)
}

func DialTCP(host string, timeout time.Duration, verbose bool) error {
	u, err := url.Parse(host)
	if err != nil {
		return fmt.Errorf("failed to parse host %q: %v", host, err)
	}

	var conn net.Conn
	address := net.JoinHostPort(u.Hostname(), u.Port())
	switch u.Scheme {
	case "tcp", "http":
		conn, err = net.DialTimeout("tcp", address, timeout)
	case "tls", "https":
		config := &tls.Config{InsecureSkipVerify: true} // #nosec G402
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: timeout}, "tcp", address, config)
	default:
		return fmt.Errorf("unsupported scheme %q", u.Scheme)
	}

	if err == nil {
		if verbose {
			klog.Infof("succeeded to dial host %q", host)
		}
		_ = conn.Close()
		return nil
	}

	return fmt.Errorf("timed out dialing host %q", host)
}

func GetNodeInternalIP(node *v1.Node) (ipv4, ipv6 string) {
	var ips []string
	for _, addr := range node.Status.Addresses {
		if addr.Type == v1.NodeInternalIP {
			ips = append(ips, addr.Address)
		}
	}

	return SplitStringIP(strings.Join(ips, ","))
}

func IsStatefulSetPod(pod *v1.Pod) (bool, string, types.UID) {
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "StatefulSet" && strings.HasPrefix(owner.APIVersion, "apps/") {
			if strings.HasPrefix(pod.Name, owner.Name) {
				return true, owner.Name, owner.UID
			}
		}
	}
	return false, "", ""
}

func GetPodType(pod *v1.Pod) string {
	if ok, _, _ := isStatefulSetPod(pod); ok {
		return consts.KindStatefulSet
	}

	//if isVMPod, _ := isVMPod(pod); isVMPod {
	//	return util.VM
	//}
	return ""
}

func isStatefulSetPod(pod *v1.Pod) (bool, string, types.UID) {
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == consts.KindStatefulSet && strings.HasPrefix(owner.APIVersion, "apps/") {
			if strings.HasPrefix(pod.Name, owner.Name) {
				return true, owner.Name, owner.UID
			}
		}
	}
	return false, "", ""
}

func GetServiceClusterIPs(svc *v1.Service) []string {
	if len(svc.Spec.ClusterIPs) == 0 && svc.Spec.ClusterIP != v1.ClusterIPNone && svc.Spec.ClusterIP != "" {
		return []string{svc.Spec.ClusterIP}
	}

	ips := make([]string, 0, len(svc.Spec.ClusterIPs))
	for _, ip := range svc.Spec.ClusterIPs {
		if net.ParseIP(ip) == nil {
			continue
		}
		ips = append(ips, ip)
	}
	return ips
}
