package daemon

import (
	"fmt"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

func (c *Controller) enqueuePod(oldObj, newObj interface{}) {

}

func (c *Controller) runPodWorker() {
	for {
		obj, shutdown := c.podQueue.Get()

		if shutdown {
			return
		}

		err := func(obj interface{}) error {
			defer c.podQueue.Done(obj)
			var key string
			var ok bool
			if key, ok = obj.(string); !ok {
				c.podQueue.Forget(obj)
				utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
				return nil
			}
			if err := c.handlePod(key); err != nil {
				c.podQueue.AddRateLimited(key)
				return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
			}
			c.podQueue.Forget(obj)
			return nil
		}(obj)
		if err != nil {
			utilruntime.HandleError(err)
		}
	}
}

func (c *Controller) handlePod(key string) error {

	return nil
}
