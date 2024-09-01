package daemon

import (
	"fmt"
	ovnv1 "github.com/fengjinlin/kube-ovn/pkg/apis/kubeovn/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

type subnetEvent struct {
	oldObj, newObj *ovnv1.Subnet
}

func (c *Controller) enqueueAddSubnet(obj interface{}) {
	c.subnetQueue.Add(subnetEvent{newObj: obj.(*ovnv1.Subnet)})
}

func (c *Controller) enqueueUpdateSubnet(oldObj, newObj interface{}) {
	c.subnetQueue.Add(subnetEvent{oldObj: oldObj.(*ovnv1.Subnet), newObj: newObj.(*ovnv1.Subnet)})
}

func (c *Controller) enqueueDeleteSubnet(obj interface{}) {
	c.subnetQueue.Add(subnetEvent{oldObj: obj.(*ovnv1.Subnet)})
}

func (c *Controller) runSubnetWorker() {
	for {
		obj, shutdown := c.subnetQueue.Get()
		if shutdown {
			return
		}

		err := func(obj interface{}) error {
			defer c.subnetQueue.Done(obj)
			event, ok := obj.(subnetEvent)
			if !ok {
				c.subnetQueue.Forget(obj)
				utilruntime.HandleError(fmt.Errorf("expected subnetEvent in workqueue but got %#v", obj))
				return nil
			}
			if err := c.reconcileSubnetRouters(&event); err != nil {
				c.subnetQueue.AddRateLimited(event)
				return fmt.Errorf("failed to sync event: %s, requeuing. oldObj: %+v, newObj: %+v", err.Error(), event.oldObj, event.newObj)
			}
			c.subnetQueue.Forget(obj)
			return nil
		}(obj)
		if err != nil {
			utilruntime.HandleError(err)
		}
	}
}

func (c *Controller) reconcileSubnetRouters(event *subnetEvent) error {

	return c.reconcileRouters()
}
