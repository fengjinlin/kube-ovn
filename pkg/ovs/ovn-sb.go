package ovs

import (
	"time"

	ovsclient "github.com/fengjinlin/kube-ovn/pkg/ovsdb/client"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnsb"
	"github.com/ovn-org/libovsdb/client"
)

type OvnSbClient struct {
	ovsDbClient
}

func NewOvnSbClient(addr string, timeout int) (*OvnSbClient, error) {
	dbModel, err := ovnsb.FullDatabaseModel()
	if err != nil {
		return nil, err
	}

	monitors := []client.MonitorOption{
		client.WithTable(&ovnsb.Chassis{}),
	}
	sbClient, err := ovsclient.NewOvsDbClient(ovsclient.SBDB, addr, dbModel, monitors)
	if err != nil {
		return nil, err
	}

	c := &OvnSbClient{
		ovsDbClient: ovsDbClient{
			Client:  sbClient,
			Timeout: time.Duration(timeout) * time.Second,
		},
	}
	return c, nil
}
