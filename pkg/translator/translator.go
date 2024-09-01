package translator

import (
	"github.com/fengjinlin/kube-ovn/pkg/ovs"
	"strings"
)

var (
	Vpc     *vpcTranslator
	Subnet  *subnetTranslator
	Node    *nodeTranslator
	Pod     *podTranslator
	IP      *ipTranslator
	Service *serviceTranslator
)

func Init(ovnNbClient ovs.OvnNbClientInterface, ovnSbClient ovs.OvnSbClientInterface) {
	base := translator{ovnNbClient, ovnSbClient}
	Vpc = &vpcTranslator{base}
	Subnet = &subnetTranslator{base}
	Node = &nodeTranslator{base}
	Pod = &podTranslator{base}
	IP = &ipTranslator{base}
	Service = &serviceTranslator{base}
}

type translator struct {
	ovnNbClient ovs.OvnNbClientInterface
	ovnSbClient ovs.OvnSbClientInterface
}

func formatModelName(name string) string {
	return strings.ReplaceAll(name, "-", ".")
}
