package ovs

import (
	"context"
	"fmt"
	"github.com/ovn-org/libovsdb/ovsdb"

	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/vswitch"
)

func (c *VSwitchClient) ListQueueByFilter(filter func(queue *vswitch.Queue) bool) ([]*vswitch.Queue, error) {
	var queues []*vswitch.Queue
	err := c.WhereCache(filter).List(context.TODO(), &queues)
	return queues, err
}

func (c *VSwitchClient) UpdateQueue(queue *vswitch.Queue, fields ...interface{}) error {
	ops, err := c.Where(queue).Update(queue, fields...)
	if err != nil {
		return fmt.Errorf("generate operations for updating queue: %v", err)
	}
	if err = c.Transact(context.TODO(), "queue-update", ops); err != nil {
		return err
	}
	return nil
}

func (c *VSwitchClient) AddQueueOps(queue *vswitch.Queue) ([]ovsdb.Operation, error) {
	return c.Create(queue)
}

func (c *VSwitchClient) GetQueueByUUID(uuid string) (*vswitch.Queue, error) {
	queue := &vswitch.Queue{
		UUID: uuid,
	}
	err := c.Get(context.TODO(), queue)
	return queue, err
}

func (c *VSwitchClient) DeleteQueueOps(queue *vswitch.Queue) ([]ovsdb.Operation, error) {
	return c.Where(queue).Delete()
}
