package controller

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	ovnipam "github.com/fengjinlin/kube-ovn/pkg/ipam"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type OvnInitializer struct {
	client.Client
	Scheme *runtime.Scheme

	Config *Configuration
}

func (oi *OvnInitializer) Start(ctx context.Context) error {
	var (
		logger = log.FromContext(ctx)
		err    error
	)

	// default vpc
	if err = oi.initDefaultVpc(ctx); err != nil {
		logger.Error(err, "failed to init default vpc")
		return err
	}

	// default subnet, use to connect pod and pod
	if err = oi.initDefaultSubnet(ctx); err != nil {
		logger.Error(err, "failed to init default subnet")
		return err
	}

	// join subnet, use to connect host and pod
	if err = oi.initJoinSubnet(ctx); err != nil {
		logger.Error(err, "failed to init join subnet")
		return err
	}

	return nil
}

func (oi *OvnInitializer) initDefaultVpc(ctx context.Context) error {
	var (
		logger  = log.FromContext(ctx)
		vpcName = oi.Config.DefaultVpc
	)
	vpc := &ovnv1.Vpc{}
	if err := oi.Get(ctx, client.ObjectKey{Name: vpcName}, vpc); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "failed to get default vpc", "vpc", vpcName)
			return err
		}

		vpc = &ovnv1.Vpc{}
		vpc.Name = vpcName
		vpc.Finalizers = []string{consts.FinalizerDefaultProtection}

		if err = oi.Create(ctx, vpc); err != nil {
			logger.Error(err, "failed to create default vpc", "vpc", vpcName)
			return err
		}

	}

	return nil
}

func (oi *OvnInitializer) initJoinSubnet(ctx context.Context) error {
	var (
		logger     = log.FromContext(ctx)
		err        error
		subnetName = oi.Config.JoinSubnet
		subnetCIDR = oi.Config.JoinSubnetCIDR
		subnetGw   = oi.Config.JoinSubnetGateway
		defaultVpc = oi.Config.DefaultVpc
	)
	subnet := &ovnv1.Subnet{}
	if err = oi.Get(ctx, client.ObjectKey{Name: subnetName}, subnet); err == nil {
		if utils.CheckProtocol(subnetCIDR) == utils.ProtocolDual &&
			utils.CheckProtocol(subnet.Spec.CIDRBlock) != utils.ProtocolDual {
			// single-stack upgrade to dual-stack
			subnet.Spec.CIDRBlock = subnetCIDR
			if err = oi.Update(ctx, subnet); err != nil {
				logger.Error(err, "failed to upgrade join subnet to dual-stack", "subnet", subnetName)
			}
		} else {
			oi.Config.JoinSubnetCIDR = subnet.Spec.CIDRBlock
		}
		return nil
	}

	if !apierrors.IsNotFound(err) {
		logger.Error(err, "failed to get join subnet", "subnet", subnetName)
		return err
	}

	subnet = &ovnv1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       subnetName,
			Finalizers: []string{consts.FinalizerDefaultProtection},
		},
		Spec: ovnv1.SubnetSpec{
			Vpc:                    defaultVpc,
			Default:                false,
			Provider:               consts.OvnProviderName,
			CIDRBlock:              subnetCIDR,
			Gateway:                subnetGw,
			GatewayNode:            "",
			ExcludeIPs:             strings.Split(subnetGw, ","),
			Protocol:               utils.CheckProtocol(subnetCIDR),
			DisableInterConnection: true,
		},
	}

	if err = oi.Create(ctx, subnet); err != nil {
		logger.Error(err, "failed to create join subnet", "subnet", subnetName)
		return err
	}

	return nil
}

func (oi *OvnInitializer) initDefaultSubnet(ctx context.Context) error {
	var (
		logger     = log.FromContext(ctx)
		err        error
		subnetName = oi.Config.DefaultSubnet
		subnetCIDR = oi.Config.DefaultSubnetCIDR
		subnetGw   = oi.Config.DefaultSubnetGateway
		defaultVpc = oi.Config.DefaultVpc
		gwCheck    = oi.Config.DefaultSubnetGatewayCheck
		excludeIps = oi.Config.DefaultSubnetExcludeIps
	)
	subnet := &ovnv1.Subnet{}
	if err = oi.Get(context.Background(), client.ObjectKey{Name: subnetName}, subnet); err == nil {
		if utils.CheckProtocol(subnet.Spec.CIDRBlock) != utils.CheckProtocol(subnetName) {
			// single-stack upgrade to dual-stack
			if utils.CheckProtocol(subnetCIDR) == utils.ProtocolDual {
				subnet.Spec.CIDRBlock = subnetCIDR
				if err = oi.Update(ctx, subnet); err != nil {
					logger.Error(err, "failed to upgrade default subnet to dual-stack", "subnet", subnet)
				}
			}
		}
		return nil
	}

	if !apierrors.IsNotFound(err) {
		logger.Error(err, "failed to get default subnet", "subnet", subnetName)
		return err
	}

	subnet = &ovnv1.Subnet{
		ObjectMeta: metav1.ObjectMeta{
			Name:       subnetName,
			Finalizers: []string{consts.FinalizerDefaultProtection},
		},
		Spec: ovnv1.SubnetSpec{
			Vpc:                 defaultVpc,
			Default:             true,
			Provider:            consts.OvnProviderName,
			CIDRBlock:           subnetCIDR,
			Gateway:             subnetGw,
			GatewayNode:         "",
			GatewayType:         consts.GatewayTypeDistributed,
			DisableGatewayCheck: !gwCheck,
			ExcludeIPs:          strings.Split(excludeIps, ","),
			NatOutgoing:         true,
			Protocol:            utils.CheckProtocol(subnetCIDR),
			EnableLb:            &oi.Config.EnableLb,
		},
	}

	if err = oi.Create(context.Background(), subnet); err != nil {
		logger.Error(err, "failed to create default subnet", "subnet", subnetName)
		return err
	}

	return nil
}

func initIPAM(ctx context.Context, client client.Reader) (*ovnipam.IPAM, error) {
	var (
		logger = log.FromContext(ctx)
		err    error

		ipam = ovnipam.NewIPAM()
	)

	var subnetList ovnv1.SubnetList
	if err = client.List(ctx, &subnetList); err != nil {
		logger.Error(err, "failed to list subnets")
		return nil, err
	}
	for _, subnet := range subnetList.Items {
		if err = ipam.AddOrUpdateSubnet(ctx, subnet.Name, subnet.Spec.CIDRBlock, subnet.Spec.Gateway, subnet.Spec.ExcludeIPs); err != nil {
			logger.Error(err, "failed to add subnet to ipam")
			return nil, err
		}
	}

	var ipList ovnv1.IPList
	if err = client.List(ctx, &ipList); err != nil {
		logger.Error(err, "failed to list ips")
		return nil, err
	}
	var ipKey string
	for _, ipCr := range ipList.Items {
		switch {
		case ipCr.Spec.PodType == consts.KindNode:
			ipKey = fmt.Sprintf("node-%s", ipCr.Spec.PodName)
		case ipCr.Spec.Namespace != "":
			ipKey = fmt.Sprintf("%s/%s", ipCr.Spec.Namespace, ipCr.Spec.PodName)
		default:
			ipKey = ipCr.Spec.PodName
		}
		if _, _, _, err = ipam.GetStaticAddress(ctx, ipKey, ipCr.Name, ipCr.Spec.IPAddress, &ipCr.Spec.MacAddress, ipCr.Spec.Subnet, true); err != nil {
			logger.Error(err, "failed to get static address", "ip", ipCr)
			return nil, err
		}
	}

	return ipam, nil
}
