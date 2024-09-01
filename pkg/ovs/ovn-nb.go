package ovs

import (
	ovsclient "github.com/fengjinlin/kube-ovn/pkg/ovsdb/client"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
	"github.com/ovn-org/libovsdb/client"
	"time"
)

const (
	logicalRouterKey      = "lr"
	logicalSwitchKey      = "ls"
	portGroupKey          = "pg"
	aclParentKey          = "parent"
	associatedSgKeyPrefix = "associated_sg_"
	securityGroupsKey     = "security_groups"
	securityGroupKey      = "security_group"
)

type OvnNbClient struct {
	ovsDbClient
}

func NewOvnNbClient(addr string, timeout int) (*OvnNbClient, error) {
	dbModel, err := ovnnb.FullDatabaseModel()
	if err != nil {
		return nil, err
	}
	monitors := []client.MonitorOption{
		client.WithTable(&ovnnb.ACL{}),
		client.WithTable(&ovnnb.AddressSet{}),
		client.WithTable(&ovnnb.BFD{}),
		client.WithTable(&ovnnb.DHCPOptions{}),
		client.WithTable(&ovnnb.GatewayChassis{}),
		client.WithTable(&ovnnb.LoadBalancer{}),
		client.WithTable(&ovnnb.LoadBalancerHealthCheck{}),
		client.WithTable(&ovnnb.LogicalRouterPolicy{}),
		client.WithTable(&ovnnb.LogicalRouterPort{}),
		client.WithTable(&ovnnb.LogicalRouterStaticRoute{}),
		client.WithTable(&ovnnb.LogicalRouter{}),
		client.WithTable(&ovnnb.LogicalSwitchPort{}),
		client.WithTable(&ovnnb.LogicalSwitch{}),
		client.WithTable(&ovnnb.NAT{}),
		client.WithTable(&ovnnb.NBGlobal{}),
		client.WithTable(&ovnnb.PortGroup{}),
	}

	nbClient, err := ovsclient.NewOvsDbClient(ovsclient.NBDB, addr, dbModel, monitors)
	if err != nil {
		return nil, err
	}

	c := &OvnNbClient{
		ovsDbClient: ovsDbClient{
			Client:  nbClient,
			Timeout: time.Duration(timeout) * time.Second,
		},
	}
	return c, nil
}
