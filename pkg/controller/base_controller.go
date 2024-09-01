package controller

import (
	"context"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/ipam"
	"github.com/fengjinlin/kube-ovn/pkg/translator"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type baseController struct {
	client.Client
	Scheme *apiruntime.Scheme

	config *Configuration
	ipam   *ipam.IPAM
}

func (r *baseController) AddOrUpdateIPCR(ctx context.Context, podName, podType, ns, ips, mac, subnetName, nodeName, providerName string) error {
	var (
		logger = log.FromContext(ctx)
		err    error

		ipName string
	)

	if subnetName == r.config.JoinSubnet {
		ipName = translator.NodeNameToPortName(nodeName)
		podName = nodeName
	} else {
		ipName = translator.PodNameToPortName(podName, ns, providerName)
	}

	var ipCr ovnv1.IP
	var exists bool
	if err = r.Get(ctx, types.NamespacedName{Name: ipName}, &ipCr); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "failed to get ip", "name", ipName)
			return err
		}
	} else {
		exists = true
	}

	v4IP, v6IP := utils.SplitStringIP(ips)
	ipLabels := map[string]string{
		consts.LabelSubnetName: subnetName,
		consts.LabelNodeName:   nodeName,
	}
	ipSpec := ovnv1.IPSpec{
		PodName:       podName,
		PodType:       podType,
		Namespace:     ns,
		Subnet:        subnetName,
		NodeName:      nodeName,
		IPAddress:     ips,
		V4IPAddress:   v4IP,
		V6IPAddress:   v6IP,
		MacAddress:    mac,
		AttachSubnets: []string{},
		AttachIPs:     []string{},
		AttachMacs:    []string{},
	}
	if !exists {
		ipCr = ovnv1.IP{
			ObjectMeta: metav1.ObjectMeta{
				Name:   ipName,
				Labels: ipLabels,
			},
			Spec: ipSpec,
		}
		if err = r.Create(ctx, &ipCr); err != nil {
			logger.Error(err, "failed to create ip", "name", ipName, "ip", ipCr)
			return err
		}
	} else {
		if !reflect.DeepEqual(ipCr.Labels, ipLabels) || !reflect.DeepEqual(ipCr.Spec, ipSpec) {
			ipCr.Labels = ipLabels
			ipCr.Spec = ipSpec
			if err = r.Update(ctx, &ipCr); err != nil {
				logger.Error(err, "failed to update ip", "name", ipName, "ip", ipCr)
				return err
			}
		}
	}

	return nil
}

func (r *baseController) DeleteIPCR(ctx context.Context, podName, ns, subnetName, nodeName, providerName string) error {
	var (
		logger = log.FromContext(ctx)
		err    error

		ipName string
	)

	if subnetName == r.config.JoinSubnet {
		ipName = translator.NodeNameToPortName(nodeName)
	} else {
		ipName = translator.PodNameToPortName(podName, ns, providerName)
	}

	ipCr := ovnv1.IP{}
	ipCr.Name = ipName
	if err = r.Delete(ctx, &ipCr); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "failed to delete ip", "name", ipName)
		}
	}
	return nil
}
