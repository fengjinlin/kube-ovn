package controller

import (
	"context"
	"errors"
	"fmt"
	v1 "k8s.io/api/core/v1"
	"net"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"

	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

func (r *subnetController) formatAndValidateSubnet(ctx context.Context, subnet *ovnv1.Subnet) error {
	var (
		logger        = log.FromContext(ctx)
		err           error
		defaultVpc    = r.config.DefaultVpc
		defaultSubnet = r.config.DefaultSubnet
		changed       = false
	)

	{ // subnet cidr
		var cidrBlock []string
		for _, cidr := range strings.Split(subnet.Spec.CIDRBlock, ",") {
			_, cidrNet, err := net.ParseCIDR(cidr)
			if err != nil {
				logger.Error(err, "failed to parse cidr", "cidr", cidr)
				return fmt.Errorf("failed to parse cidr: %s", cidr)
			}
			if cidrNet.String() != cidr {
				changed = true
			}
			cidrBlock = append(cidrBlock, cidrNet.String())
		}
		subnet.Spec.CIDRBlock = strings.Join(cidrBlock, ",")

		if err = utils.CIDRGlobalUnicast(subnet.Spec.CIDRBlock); err != nil {
			return err
		}

		protocol := utils.CheckProtocol(subnet.Spec.CIDRBlock)
		if protocol == "" {
			return fmt.Errorf("failed to format cidr: %s", subnet.Spec.CIDRBlock)
		}
		if subnet.Spec.Protocol != protocol {
			subnet.Spec.Protocol = protocol
			changed = true
		}
	}

	// validate cidr conflict
	{
		// conflict with other subnet cidr
		var subnetList ovnv1.SubnetList
		if err = r.List(ctx, &subnetList); err != nil {
			logger.Error(err, "failed to list subnets")
			return errors.New("failed to list subnets")
		}
		for _, sub := range subnetList.Items {
			if sub.Spec.Vpc != subnet.Spec.Vpc || sub.Spec.Vlan != subnet.Spec.Vlan || sub.Name == subnet.Name {
				continue
			}

			if utils.CIDROverlap(sub.Spec.CIDRBlock, subnet.Spec.CIDRBlock) {
				logger.Error(nil, "subnet cidr conflicts with other subnet", "subnet", sub.Name, "cidr", sub.Spec.CIDRBlock)
				return fmt.Errorf("subnet cidr conflicts with other subnet: name=%s, cidr=%s", sub.Name, sub.Spec.CIDRBlock)
			}
		}
		// conflict with node addr
		var nodeList v1.NodeList
		if err = r.List(ctx, &nodeList); err != nil {
			logger.Error(err, "failed to list nodes")
			return errors.New("failed to list nodes")
		}
		for _, node := range nodeList.Items {
			for _, addr := range node.Status.Addresses {
				if addr.Type == v1.NodeInternalIP && utils.CIDRContainIP(subnet.Spec.CIDRBlock, addr.Address) {
					logger.Error(nil, "subnet cidr conflicts with node address", "node", node.Name, "address", addr.Address)
					return fmt.Errorf("subnet cidr conflicts with node address: node=%s, addr=%s", node.Name, addr.Address)
				}
			}
		}
		// conflict with k8s svc ip
		k8sAPIServer := os.Getenv("KUBERNETES_SERVICE_HOST")
		if k8sAPIServer != "" && utils.CIDRContainIP(subnet.Spec.CIDRBlock, k8sAPIServer) {
			logger.Error(nil, "subnet cidr conflicts with k8s apiserver svc ip", "svcIP", k8sAPIServer)
			return fmt.Errorf("subnet cidr conflicts with k8s apiserver svc ip: %s", k8sAPIServer)
		}
	}

	{ // gateway
		var gw string

		switch {
		case subnet.Spec.Gateway == "":
			gw, err = utils.GetGwByCidr(subnet.Spec.CIDRBlock)
		case subnet.Spec.Protocol == utils.ProtocolDual &&
			utils.CheckProtocol(subnet.Spec.CIDRBlock) != utils.CheckProtocol(subnet.Spec.Gateway):
			gw, err = utils.AppendGwByCidr(subnet.Spec.Gateway, subnet.Spec.CIDRBlock)
		default:
			gw = subnet.Spec.Gateway
		}

		if err != nil {
			logger.Error(err, "failed to check and update subnet gateway", "gateway", subnet.Spec.Gateway, "cidr", subnet.Spec.CIDRBlock)
			return fmt.Errorf("failed to check and update subnet gateway: gateway=%s, cidr=%s", subnet.Spec.Gateway, subnet.Spec.CIDRBlock)
		}

		if subnet.Spec.Gateway != gw {
			subnet.Spec.Gateway = gw
			changed = true
		}

		if !utils.CIDRContainIP(subnet.Spec.CIDRBlock, subnet.Spec.Gateway) {
			return fmt.Errorf(" gateway %s is not in cidr %s", subnet.Spec.Gateway, subnet.Spec.CIDRBlock)
		}

	}

	{ // gateway type
		if subnet.Spec.GatewayType == "" {
			subnet.Spec.GatewayType = consts.GatewayTypeDistributed
			changed = true
		} else {
			if subnet.Spec.GatewayType != consts.GatewayTypeDistributed &&
				subnet.Spec.GatewayType != consts.GatewayTypeCentralized {
				return fmt.Errorf("%s is not a valid gateway type", subnet.Spec.GatewayType)
			}
		}
	}

	{ // exclude ips
		for _, ipr := range subnet.Spec.ExcludeIPs {
			ips := strings.Split(ipr, "..")
			if len(ips) > 2 {
				return fmt.Errorf("%s in excludeIps is not a valid ip range", ipr)
			}

			if len(ips) == 1 {
				if net.ParseIP(ips[0]) == nil {
					return fmt.Errorf("ip %s in excludeIps is not a valid address", ips[0])
				}
			}

			if len(ips) == 2 {
				for _, ip := range ips {
					if net.ParseIP(ip) == nil {
						return fmt.Errorf("ip %s in excludeIps is not a valid address", ip)
					}
				}
				if utils.IP2BigInt(ips[0]).Cmp(utils.IP2BigInt(ips[1])) == 1 {
					return fmt.Errorf("%s in excludeIps is not a valid ip range", ipr)
				}
			}
		}

		for _, gw := range strings.Split(subnet.Spec.Gateway, ",") {
			if !utils.ContainsString(subnet.Spec.ExcludeIPs, gw) {
				subnet.Spec.ExcludeIPs = append(subnet.Spec.ExcludeIPs, gw)
				changed = true
			}
		}
	}

	{ // provider
		if subnet.Spec.Provider == "" {
			subnet.Spec.Provider = consts.OvnProviderName
			changed = true
		}
	}

	{ // vpc
		if subnet.Spec.Vpc == "" {
			subnet.Spec.Vpc = defaultVpc
			changed = true

			// Some features only work in the default VPC
			if subnet.Spec.Default && subnet.Name != defaultSubnet {
				subnet.Spec.Default = false
				changed = true
			}
		}
	}

	logger.Info("format and validate subnet done", "changed", changed)
	if changed {
		if err = r.Update(ctx, subnet); err != nil {
			logger.Error(err, "failed to update subnet")
			return errors.New("failed to update subnet")
		}
	}

	return nil
}

func (r *subnetController) validateVpcBySubnet(ctx context.Context, subnet *ovnv1.Subnet) (*ovnv1.Vpc, error) {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	vpc := &ovnv1.Vpc{}
	if err = r.Get(ctx, client.ObjectKey{Name: subnet.Spec.Vpc}, vpc); err != nil {
		logger.Error(err, "failed to get vpc")
		return nil, err
	}

	if !vpc.Status.Ready {
		logger.Error(nil, "vpc not ready yet")
		return nil, fmt.Errorf("vpc not ready yet")
	}

	if !vpc.Status.Default {
		for _, ns := range subnet.Spec.Namespaces {
			if !utils.ContainsString(vpc.Spec.Namespaces, ns) {
				logger.Error(nil, "namespace is out of range to vpc", "namespace", ns, "vpcNamespaces", vpc.Spec.Namespaces)
				return nil, errors.New("namespace is out of range to vpc")
			}
		}
		//} else {
		//	vpcList := &ovnv1.VpcList{}
		//	if err := r.List(ctx, vpcList); err != nil {
		//		return vpc, err
		//	}
		//	for _, vpc := range vpcList.Items {
		//		if (subnet.Annotations[consts.AnnotationVpcLastName] == "" && subnet.Spec.Vpc != vpc.Name ||
		//			subnet.Annotations[consts.AnnotationVpcLastName] != "" && subnet.Annotations[consts.AnnotationVpcLastName] != vpc.Name) &&
		//			!vpc.Status.Default && utils.IsStringsOverlap(vpc.Spec.Namespaces, subnet.Spec.Namespaces) {
		//			err = fmt.Errorf("namespaces %v are overlap with vpc '%s'", subnet.Spec.Namespaces, vpc.Name)
		//			return vpc, err
		//		}
		//	}
	}

	return vpc, nil
}
