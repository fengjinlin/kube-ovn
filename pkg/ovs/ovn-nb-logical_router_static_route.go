package ovs

import (
	"context"
	"errors"
	"fmt"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
	"github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func (c *OvnNbClient) DeleteLogicalRouterStaticRouteByUUID(ctx context.Context, lrName, uuid string) error {
	mutationFunc := func(lr *ovnnb.LogicalRouter) *model.Mutation {
		return &model.Mutation{
			Field:   &lr.StaticRoutes,
			Value:   []string{uuid},
			Mutator: ovsdb.MutateOperationDelete,
		}
	}
	ops, err := c.MutateLogicalRouterOps(ctx, lrName, mutationFunc)
	if err != nil {
		return err
	}

	if err = c.Transact(ctx, "lr-static-route-del", ops); err != nil {
		return err
	}

	return nil
}

func (c *OvnNbClient) AddLogicalRouterStaticRoute(ctx context.Context, lrName string, routeToAdd *ovnnb.LogicalRouterStaticRoute) error {
	logger := log.FromContext(ctx)

	if routeToAdd.Policy == nil {
		routeToAdd.Policy = &ovnnb.LogicalRouterStaticRoutePolicyDstIP
	}

	filter := func(route *ovnnb.LogicalRouterStaticRoute) bool {
		return route.RouteTable == route.RouteTable && *route.Policy == *route.Policy && route.IPPrefix == route.IPPrefix
	}
	routes, err := c.ListLogicalRouterStaticRoutes(ctx, lrName, filter)
	if err != nil {
		return err
	}

	var exists bool
	for _, route := range routes {
		if !exists && reflect.DeepEqual(route.ExternalIDs, routeToAdd.ExternalIDs) {
			exists = true
		} else {
			if route.BFD != nil && routeToAdd.BFD != nil && *route.BFD != *routeToAdd.BFD {
				continue
			}
			if err = c.DeleteLogicalRouterStaticRouteByUUID(ctx, lrName, route.UUID); err != nil {
				logger.Error(err, "failed to delete logical router static route", "uuid", route.UUID)
				return err
			}
		}
	}

	if !exists {
		routeToAdd.UUID = c.NamedUUID()
		if routeToAdd.BFD != nil {
			routeToAdd.Options = map[string]string{consts.StaticRouteBfdEcmp: "true"}
		}
		if err = c.addLogicalRouterStaticRoutes(ctx, lrName, []*ovnnb.LogicalRouterStaticRoute{routeToAdd}); err != nil {
			return err
		}
	}

	return nil
}

func (c *OvnNbClient) addLogicalRouterStaticRoutes(ctx context.Context, lrName string, routes []*ovnnb.LogicalRouterStaticRoute) error {
	if len(routes) == 0 {
		return nil
	}

	models := make([]model.Model, 0, len(routes))
	uuids := make([]string, 0, len(routes))
	for _, route := range routes {
		models = append(models, model.Model(route))
		uuids = append(uuids, route.UUID)
	}
	routeCreateOps, err := c.Create(models...)
	if err != nil {
		return fmt.Errorf("generate operations for creating static routes: %v", err)
	}

	mutation := func(lr *ovnnb.LogicalRouter) *model.Mutation {
		return &model.Mutation{
			Field:   &lr.StaticRoutes,
			Mutator: ovsdb.MutateOperationInsert,
			Value:   uuids,
		}
	}

	lrUpdateOps, err := c.MutateLogicalRouterOps(ctx, lrName, mutation)
	if err != nil {
		return fmt.Errorf("generate operations for adding static routes to logical router %s: %v", lrName, err)
	}

	ops := make([]ovsdb.Operation, 0, len(routeCreateOps)+len(lrUpdateOps))
	ops = append(ops, routeCreateOps...)
	ops = append(ops, lrUpdateOps...)

	if err = c.Transact(ctx, "lr-static-route-add", ops); err != nil {
		return err
	}

	return nil
}

func (c *OvnNbClient) ListLogicalRouterStaticRoutes(ctx context.Context, lrName string, filter func(*ovnnb.LogicalRouterStaticRoute) bool) ([]*ovnnb.LogicalRouterStaticRoute, error) {
	lr, err := c.GetLogicalRouter(ctx, lrName, false)
	if err != nil {
		return nil, err
	}

	routes := make([]*ovnnb.LogicalRouterStaticRoute, 0, len(lr.StaticRoutes))
	for _, id := range lr.StaticRoutes {
		if route, err := c.getLogicalRouterStaticRouteByUUID(ctx, id); err != nil {
			if errors.Is(err, client.ErrNotFound) {
				continue
			}
			return nil, err
		} else {
			if filter == nil || filter(route) {
				routes = append(routes, route)
			}
		}
	}

	return routes, nil
}

func (c *OvnNbClient) getLogicalRouterStaticRouteByUUID(ctx context.Context, uuid string) (*ovnnb.LogicalRouterStaticRoute, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	route := &ovnnb.LogicalRouterStaticRoute{UUID: uuid}
	if err := c.Get(ctx, route); err != nil {
		return nil, err
	}

	return route, nil
}
