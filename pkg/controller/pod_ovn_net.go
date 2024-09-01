package controller

import (
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
)

const (
	providerTypeIpam = iota
	providerTypeOriginal
)

type ovnNet struct {
	providerType int
	providerName string
	subnet       *ovnv1.Subnet
	isDefault    bool
}

func (r *podController) getPodOvnNets(ctx context.Context, pod *corev1.Pod) ([]*ovnNet, error) {
	var (
		logger = log.FromContext(ctx)
		nets   []*ovnNet
	)

	subnet, err := r.getPodDefaultSubnet(ctx, pod)
	if err != nil {
		logger.Error(err, "failed to get default subnet of pod")
		return nil, err
	}
	defaultOvnNet := &ovnNet{
		providerType: providerTypeOriginal,
		providerName: consts.OvnProviderName,
		subnet:       subnet,
		isDefault:    true,
	}

	nets = append(nets, defaultOvnNet)

	return nets, nil
}

func (r *podController) getPodDefaultSubnet(ctx context.Context, pod *corev1.Pod) (*ovnv1.Subnet, error) {
	var (
		logger = log.FromContext(ctx)
		err    error
		subnet ovnv1.Subnet
	)

	if pod.Annotations != nil && pod.Annotations[consts.AnnotationSubnet] != "" {
		subnetName := pod.Annotations[consts.AnnotationSubnet]
		if err = r.Get(ctx, client.ObjectKey{Name: subnetName}, &subnet); err != nil {
			logger.Error(err, "failed to get subnet", "subnet", subnetName)
			return nil, err
		}
		return &subnet, nil
	} else {
		var ns corev1.Namespace
		if err = r.Get(ctx, client.ObjectKey{Name: pod.Namespace}, &ns); err != nil {
			logger.Error(err, "failed to get pod namespace")
			return nil, err
		}
		if ns.Annotations == nil || ns.Annotations[consts.AnnotationSubnet] == "" {
			logger.Error(nil, "default subnet of pod namespace not found")
			return nil, errors.New("default subnet of pod namespace not found")
		}

		subnetName := ns.Annotations[consts.AnnotationSubnet]
		if err = r.Get(ctx, client.ObjectKey{Name: subnetName}, &subnet); err != nil {
			logger.Error(err, "failed to get subnet", "subnet", subnetName)
			return nil, err
		}
	}

	return &subnet, nil
}
