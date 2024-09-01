package ovs

import (
	"context"
	"fmt"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sync/atomic"
	"time"

	"github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/ovsdb"
)

type ovsDbClient struct {
	client.Client
	Timeout time.Duration
}

func (c *ovsDbClient) Transact(ctx context.Context, method string, operations []ovsdb.Operation) error {
	logger := log.FromContext(ctx)
	logger = logger.WithValues("method", method)

	if len(operations) == 0 {
		logger.Info("operations should not be empty")
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	start := time.Now()
	_, err := c.Client.Transact(ctx, operations...)
	elapsed := float64((time.Since(start)) / time.Millisecond)

	//var dbType string
	//switch c.Schema().Name {
	//case "OVN_Northbound":
	//	dbType = "kube-ovn-nb"
	//case "OVN_Southbound":
	//	dbType = "kube-ovn-sb"
	//default:
	//	dbType = c.Schema().Name
	//}

	logger = logger.WithValues("dbType", c.Schema().Name)

	if err != nil {
		logger.Error(err, "error occurred in transact", "operations", operations)
		return err
	}

	if elapsed > 500 {
		logger.Info("operations took too long", "operations", operations, "elapsed", elapsed)
	}

	return nil
}

var namedUUIDCounter uint32

func (c *ovsDbClient) NamedUUID() string {
	return fmt.Sprintf("u%010d", atomic.AddUint32(&namedUUIDCounter, 1))
}
