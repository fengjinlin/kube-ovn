package translator

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type serviceTranslator struct {
	translator
}

func (t *serviceTranslator) AddServiceLoadBalancerVips(ctx context.Context, svc *corev1.Service, endpoints *corev1.Endpoints, vpc *ovnv1.Vpc) error {
	var (
		logger = log.FromContext(ctx)
		err    error

		sessionAffinity = svc.Spec.SessionAffinity == corev1.ServiceAffinityClientIP
		vipsMap         = make(map[corev1.Protocol][]string) // protocol: []string{vip}
		svcClusterIPs   = utils.GetServiceClusterIPs(svc)
	)

	if len(svcClusterIPs) == 0 { // headless
		return nil
	}

	for _, clusterIP := range svcClusterIPs {
		for _, port := range svc.Spec.Ports {

			var (
				vip         = utils.JoinHostPort(clusterIP, port.Port)
				backends    []string
				vipProtocol = utils.CheckProtocol(clusterIP)
			)
			if _, ok := vipsMap[port.Protocol]; !ok {
				vipsMap[port.Protocol] = make([]string, 0)
			}
			vipsMap[port.Protocol] = append(vipsMap[port.Protocol], vip)

			lbToAdd, lbToDel := t.getVpcLoadBalancer(vpc, port.Protocol, sessionAffinity)

			for _, subset := range endpoints.Subsets {
				var targetPort int32
				for _, p := range subset.Ports {
					if p.Name == port.Name {
						targetPort = p.Port
						break
					}
				}
				if targetPort == 0 {
					continue
				}
				for _, addr := range subset.Addresses {
					if utils.CheckProtocol(addr.IP) == vipProtocol {
						backends = append(backends, utils.JoinHostPort(addr.IP, targetPort))
					}
				}
			}
			if len(backends) > 0 {
				logger.Info("add vip to load balancer", "lb", lbToAdd, "vip", vip, "backends", backends)
				if err = t.ovnNbClient.AddLoadBalancerVip(ctx, lbToAdd, vip, backends...); err != nil {
					logger.Error(err, "failed to add vip to load balancer")
				}
			} else {
				logger.Info("no backends, delete vip from load balancer", "lb", lbToAdd, "vip", vip)
				if err = t.ovnNbClient.DeleteLoadBalancerVip(ctx, lbToAdd, vip); err != nil {
					logger.Error(err, "failed to delete vip from load balancer")
				}
			}
			logger.Info("delete stale vip from load balancer", "lb", lbToDel, "vip", vip)
			if err = t.ovnNbClient.DeleteLoadBalancerVip(ctx, lbToDel, vip); err != nil {
				logger.Error(err, "failed to delete old vip from load balancer")
			}
		}
	}

	// clear invalid vips
	for protocol, vips := range vipsMap {
		lbName, _ := t.getVpcLoadBalancer(vpc, protocol, sessionAffinity)
		lb, err := t.ovnNbClient.GetLoadBalancer(ctx, lbName, false)
		if err != nil {
			logger.Error(err, "failed to get load balancer", "lb", lbName)
			return err
		}
		if lb.ExternalIDs != nil && lb.ExternalIDs[fmt.Sprintf(consts.ExternalIDsKeyServiceName, svc.Name)] != "" {
			lastVips := lb.ExternalIDs[fmt.Sprintf(consts.ExternalIDsKeyServiceName, svc.Name)]
			for _, v1 := range strings.Split(lastVips, ",") {
				var exists bool
				for _, v2 := range vips {
					if v1 == v2 {
						exists = true
						break
					}
				}
				if !exists {
					if err = t.ovnNbClient.DeleteLoadBalancerVip(ctx, lbName, v1); err != nil {
						logger.Error(err, "failed to delete stale vip from load balancer", "lb", lbName, "vip", v1)
						return err
					}
				}
			}

			if lb.ExternalIDs == nil {
				lb.ExternalIDs = make(map[string]string)
			}
			sort.Strings(vips)
			vipsStr := strings.Join(vips, ",")
			svcKey := fmt.Sprintf(consts.ExternalIDsKeyServiceName, svc.Name)
			if lb.ExternalIDs[svcKey] != vipsStr {
				lb.ExternalIDs[svcKey] = vipsStr
				if err = t.ovnNbClient.UpdateLoadBalancer(ctx, lb, &lb.ExternalIDs); err != nil {
					logger.Error(err, "failed to set load balancer externalIds", "lb", lbName)
					return err
				}
			}
		}
	}
	return nil
}

func (t *serviceTranslator) getVpcLoadBalancer(vpc *ovnv1.Vpc, protocol corev1.Protocol, sessionAffinity bool) (string, string) {
	var lbToAdd, lbToDel string

	switch protocol {
	case corev1.ProtocolTCP:
		if sessionAffinity {
			lbToAdd = vpc.Status.TCPSessionLoadBalancer
			lbToDel = vpc.Status.TCPLoadBalancer
		} else {
			lbToAdd = vpc.Status.TCPLoadBalancer
			lbToDel = vpc.Status.TCPSessionLoadBalancer
		}
	case corev1.ProtocolUDP:
		if sessionAffinity {
			lbToAdd = vpc.Status.UDPSessionLoadBalancer
			lbToDel = vpc.Status.UDPLoadBalancer
		} else {
			lbToAdd = vpc.Status.UDPLoadBalancer
			lbToDel = vpc.Status.UDPSessionLoadBalancer
		}
	case corev1.ProtocolSCTP:
		if sessionAffinity {
			lbToAdd = vpc.Status.SctpSessionLoadBalancer
			lbToDel = vpc.Status.SctpLoadBalancer
		} else {
			lbToAdd = vpc.Status.SctpLoadBalancer
			lbToDel = vpc.Status.SctpSessionLoadBalancer
		}
	}

	return lbToAdd, lbToDel
}

func (t *serviceTranslator) DeleteServiceLoadBalancerVips(ctx context.Context, svc *corev1.Service, vpc *ovnv1.Vpc) error {
	var (
		logger = log.FromContext(ctx)
		err    error

		svcName = svc.Name
	)

	if err = t.deleteServiceVips(ctx, svcName, vpc.Status.TCPLoadBalancer); err != nil {
		logger.Error(err, "failed to delete services lb vips", "lb", vpc.Status.TCPLoadBalancer)
	}
	if err = t.deleteServiceVips(ctx, svcName, vpc.Status.UDPLoadBalancer); err != nil {
		logger.Error(err, "failed to delete services lb vips", "lb", vpc.Status.UDPLoadBalancer)
	}
	if err = t.deleteServiceVips(ctx, svcName, vpc.Status.SctpLoadBalancer); err != nil {
		logger.Error(err, "failed to delete services lb vips", "lb", vpc.Status.SctpLoadBalancer)
	}
	if err = t.deleteServiceVips(ctx, svcName, vpc.Status.TCPSessionLoadBalancer); err != nil {
		logger.Error(err, "failed to delete services lb vips", "lb", vpc.Status.TCPSessionLoadBalancer)
	}
	if err = t.deleteServiceVips(ctx, svcName, vpc.Status.UDPSessionLoadBalancer); err != nil {
		logger.Error(err, "failed to delete services lb vips", "lb", vpc.Status.UDPSessionLoadBalancer)
	}
	if err = t.deleteServiceVips(ctx, svcName, vpc.Status.SctpSessionLoadBalancer); err != nil {
		logger.Error(err, "failed to delete services lb vips", "lb", vpc.Status.SctpSessionLoadBalancer)
	}

	return nil
}

func (t *serviceTranslator) deleteServiceVips(ctx context.Context, svcName, lbName string) error {
	var (
		logger = log.FromContext(ctx).WithValues("service", svcName, "lb", lbName)
		err    error
	)
	var lb *ovnnb.LoadBalancer
	if lb, err = t.ovnNbClient.GetLoadBalancer(ctx, lbName, true); err != nil {
		logger.Error(err, "failed to get load balancer")
		return err
	}
	if lb == nil {
		logger.Info("load balancer not found")
		return nil
	}

	svcKey := fmt.Sprintf(consts.ExternalIDsKeyServiceName, svcName)
	if lb.ExternalIDs == nil || lb.ExternalIDs[svcKey] == "" {
		logger.Info("vips not found")
		return nil
	}

	vipsToDelete := make(map[string]string)
	for _, vip := range strings.Split(lb.ExternalIDs[svcKey], ",") {
		if val, ok := lb.ExternalIDs[vip]; ok {
			vipsToDelete[vip] = val
		}
	}

	if len(vipsToDelete) > 0 {
		mutationFunc := func(lb *ovnnb.LoadBalancer) []*model.Mutation {
			return []*model.Mutation{
				{
					Field:   &lb.Vips,
					Mutator: ovsdb.MutateOperationDelete,
					Value:   vipsToDelete,
				},
			}
		}
		var ops []ovsdb.Operation
		if ops, err = t.ovnNbClient.MutateLoadBalancerOp(ctx, lbName, mutationFunc); err != nil {
			logger.Error(err, "failed to generate operations for deleting service vips")
			return err
		}
		if err = t.ovnNbClient.Transact(ctx, "lb-vip-del", ops); err != nil {
			return err
		}
	}

	return nil
}
