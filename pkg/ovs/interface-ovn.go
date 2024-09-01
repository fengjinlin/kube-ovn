package ovs

import (
	"context"

	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"

	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
)

type OvnNbClientInterface interface {
	LogicalRouter
	LogicalRouterPort
	LogicalRouterPolicy
	LogicalRouterStaticRoute

	LogicalSwitch
	LogicalSwitchPort

	PortGroup

	LoadBalancer

	Common
}

type OvnSbClientInterface interface{}

type LogicalRouter interface {
	CreateLogicalRouter(ctx context.Context, lrName string) error
	DeleteLogicalRouter(ctx context.Context, lrName string) error
	GetLogicalRouter(ctx context.Context, lrName string, ignoreNotFound bool) (*ovnnb.LogicalRouter, error)
	MutateLogicalRouterOps(ctx context.Context, lrName string, mutationFunc ...func(lr *ovnnb.LogicalRouter) *model.Mutation) ([]ovsdb.Operation, error)
	UpdateLogicalRouterOps(ctx context.Context, lr *ovnnb.LogicalRouter, fields ...interface{}) ([]ovsdb.Operation, error)
	//ClearLogicalRouterPolicy(ctx context.Context, lrName string) error
}

type LogicalRouterPort interface {
	GetLogicalRouterPort(ctx context.Context, lrpName string, ignoreNotFound bool) (*ovnnb.LogicalRouterPort, error)
	CreateLogicalRouterPortOp(ctx context.Context, lrName string, lrp *ovnnb.LogicalRouterPort) ([]ovsdb.Operation, error)
	UpdateLogicalRouterPort(ctx context.Context, lrp *ovnnb.LogicalRouterPort, fields ...interface{}) error
	UpdateLogicalRouterPortOp(ctx context.Context, lrp *ovnnb.LogicalRouterPort, fields ...interface{}) ([]ovsdb.Operation, error)
	DeleteLogicalRouterPortOp(ctx context.Context, lrpName string) ([]ovsdb.Operation, error)
}

type LogicalRouterPolicy interface {
	AddLogicalRouterPolicy(ctx context.Context, router string, policy *ovnnb.LogicalRouterPolicy) error
	GetLogicalRouterPolicyByUUID(ctx context.Context, uuid string) (*ovnnb.LogicalRouterPolicy, error)
	ListLogicalRouterPolicy(ctx context.Context, lrName string, filter func(policy *ovnnb.LogicalRouterPolicy) bool) ([]*ovnnb.LogicalRouterPolicy, error)
	DeleteLogicalRouterPolicyByFilter(ctx context.Context, lrName string, filter func(policy *ovnnb.LogicalRouterPolicy) bool) error
	DeleteLogicalRouterPolicyByUUID(ctx context.Context, lrName string, policyUUID string) error
}

type LogicalRouterStaticRoute interface {
	ListLogicalRouterStaticRoutes(ctx context.Context, lrName string, filter func(*ovnnb.LogicalRouterStaticRoute) bool) ([]*ovnnb.LogicalRouterStaticRoute, error)
	AddLogicalRouterStaticRoute(ctx context.Context, lrName string, route *ovnnb.LogicalRouterStaticRoute) error
	DeleteLogicalRouterStaticRouteByUUID(ctx context.Context, lrName, uuid string) error
}

type LogicalSwitch interface {
	GetLogicalSwitch(ctx context.Context, lsName string, ignoreNotFound bool) (*ovnnb.LogicalSwitch, error)
	CreateLogicalSwitch(ctx context.Context, lsName string) error
	MutateLogicalSwitchOp(ctx context.Context, lsName string, mutationFunc ...func(ls *ovnnb.LogicalSwitch) *model.Mutation) ([]ovsdb.Operation, error)
	DeleteLogicalSwitch(ctx context.Context, lsName string) error
}

type LogicalSwitchPort interface {
	CreateBareLogicalSwitchPort(ctx context.Context, lsName, lspName, ip, mac string) error
	GetLogicalSwitchPort(ctx context.Context, lspName string, ignoreNotFound bool) (*ovnnb.LogicalSwitchPort, error)
	DeleteLogicalSwitchPort(ctx context.Context, lspName string) error
	DeleteLogicalSwitchPortOp(ctx context.Context, lspName string) ([]ovsdb.Operation, error)
	CreateLogicalSwitchPortOp(ctx context.Context, lsName string, lsp *ovnnb.LogicalSwitchPort) ([]ovsdb.Operation, error)
	ListLogicalSwitchPorts(ctx context.Context, filter func(lsp *ovnnb.LogicalSwitchPort) bool) ([]ovnnb.LogicalSwitchPort, error)
}

type PortGroup interface {
	CreatePortGroup(ctx context.Context, pgName string, externalIDs map[string]string) error
	GetPortGroup(ctx context.Context, pgName string, ignoreNotFound bool) (*ovnnb.PortGroup, error)
	DeletePortGroup(ctx context.Context, pgName string) error
	MutatePortGroupOp(ctx context.Context, pgName string, mutationFunc ...func(ls *ovnnb.PortGroup) *model.Mutation) ([]ovsdb.Operation, error)
	AddPortGroupPorts(ctx context.Context, pgName string, lspNames ...string) error
	AddPortGroupPortsOp(ctx context.Context, pgName string, lspNames ...string) ([]ovsdb.Operation, error)
}

type LoadBalancer interface {
	GetLoadBalancer(ctx context.Context, lbName string, ignoreNotFound bool) (*ovnnb.LoadBalancer, error)
	CreateLoadBalancer(ctx context.Context, lb *ovnnb.LoadBalancer) error
	UpdateLoadBalancer(ctx context.Context, lb *ovnnb.LoadBalancer, fields ...interface{}) error
	DeleteLoadBalancer(ctx context.Context, lbName string) error
	MutateLoadBalancerOp(ctx context.Context, lbName string, mutationFunc ...func(lb *ovnnb.LoadBalancer) []*model.Mutation) ([]ovsdb.Operation, error)
	AddLoadBalancerVip(ctx context.Context, lbName, vip string, backends ...string) error
	DeleteLoadBalancerVip(ctx context.Context, lbName, vip string) error
}
