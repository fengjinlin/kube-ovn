package ovs

import (
	ovsclient "github.com/fengjinlin/kube-ovn/pkg/ovsdb/client"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/vswitch"
	"github.com/ovn-org/libovsdb/client"
	"time"
)

type VSwitchClient struct {
	ovsDbClient
}

func NewVSwitchClient(addr string, timeout int) (*VSwitchClient, error) {
	dbModel, err := vswitch.FullDatabaseModel()
	if err != nil {
		return nil, err
	}

	monitors := []client.MonitorOption{
		client.WithTable(&vswitch.Bridge{}),
		client.WithTable(&vswitch.Port{}),
		client.WithTable(&vswitch.Interface{}),
		client.WithTable(&vswitch.Mirror{}),
		client.WithTable(&vswitch.OpenvSwitch{}),
		client.WithTable(&vswitch.QoS{}),
		client.WithTable(&vswitch.Queue{}),
		client.WithTable(&vswitch.SSL{}),
	}

	cli, err := ovsclient.NewOvsDbClient(ovsclient.VSWITCH, addr, dbModel, monitors)
	if err != nil {
		return nil, err
	}

	c := &VSwitchClient{
		ovsDbClient: ovsDbClient{
			Client:  cli,
			Timeout: time.Duration(timeout) * time.Second,
		},
	}
	return c, nil
}
