package ovs

import (
	"context"
	"errors"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
	"github.com/ovn-org/libovsdb/client"
	"github.com/ovn-org/libovsdb/model"
	"github.com/ovn-org/libovsdb/ovsdb"
	"github.com/scylladb/go-set/strset"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func (c *OvnNbClient) AddLogicalRouterPolicy(ctx context.Context, lrName string, policyToAdd *ovnnb.LogicalRouterPolicy) error {
	var (
		logger = log.FromContext(ctx)
		err    error
	)
	filter := func(policy *ovnnb.LogicalRouterPolicy) bool {
		return policy.Priority == policyToAdd.Priority && policy.Match == policyToAdd.Match
	}
	policies, err := c.ListLogicalRouterPolicy(ctx, lrName, filter)
	if err != nil {
		logger.Error(err, "failed to list logical router policy")
		return err
	}

	var found bool
	for _, policy := range policies {
		if !found &&
			policy.Action == policyToAdd.Action &&
			reflect.DeepEqual(policy.ExternalIDs, policyToAdd.ExternalIDs) &&
			(policy.Action == ovnnb.LogicalRouterPolicyActionReroute && strset.New(policy.Nexthops...).IsEqual(strset.New(policyToAdd.Nexthops...))) {
			found = true
		} else {
			logger.Info("delete duplicate policy", "policy", policy)
			err = c.deleteLogicalRouterPolicyByUUID(ctx, lrName, policy.UUID)
			if err != nil {
				logger.Error(err, "failed to delete logical router policy", "uuid", policy.UUID)
				return err
			}
		}
	}

	if !found {
		policyToAdd.UUID = c.NamedUUID()

		err = c.addLogicalRouterPolicies(ctx, lrName, policyToAdd)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *OvnNbClient) addLogicalRouterPolicies(ctx context.Context, lrName string, policies ...*ovnnb.LogicalRouterPolicy) error {
	logger := log.FromContext(ctx)

	if len(policies) == 0 {
		return nil
	}
	models := make([]model.Model, 0, len(policies))
	uuids := make([]string, 0, len(policies))
	for _, policy := range policies {
		if policy.UUID == "" {
			policy.UUID = c.NamedUUID()
		}
		models = append(models, model.Model(policy))
		uuids = append(uuids, policy.UUID)
	}

	policyAddOps, err := c.ovsDbClient.Create(models...)
	if err != nil {
		logger.Error(err, "failed to generate operations for adding logical router policy")
		return err
	}

	lrUpdateOps, err := c.MutateLogicalRouterOps(ctx, lrName, func(lr *ovnnb.LogicalRouter) *model.Mutation {
		return &model.Mutation{
			Field:   &lr.Policies,
			Value:   uuids,
			Mutator: ovsdb.MutateOperationInsert,
		}
	})
	if err != nil {
		logger.Error(err, "failed to generate operations for adding logical router policy")
		return err
	}

	ops := make([]ovsdb.Operation, 0, len(policyAddOps)+len(lrUpdateOps))
	ops = append(ops, policyAddOps...)
	ops = append(ops, lrUpdateOps...)

	if err = c.Transact(ctx, "lr-policy-add", ops); err != nil {
		return err
	}

	return nil
}

func (c *OvnNbClient) getLogicalRouterPolicyByUUID(uuid string) (*ovnnb.LogicalRouterPolicy, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	policy := &ovnnb.LogicalRouterPolicy{
		UUID: uuid,
	}
	if err := c.Get(ctx, policy); err != nil {
		return nil, err
	}

	return policy, nil
}

func (c *OvnNbClient) deleteLogicalRouterPolicyByUUID(ctx context.Context, lrName, uuid string) error {
	logger := log.FromContext(ctx)

	mutationFunc := func(lr *ovnnb.LogicalRouter) *model.Mutation {
		return &model.Mutation{
			Field:   &lr.Policies,
			Value:   []string{uuid},
			Mutator: ovsdb.MutateOperationDelete,
		}
	}

	ops, err := c.MutateLogicalRouterOps(ctx, lrName, mutationFunc)
	if err != nil {
		logger.Error(err, "failed to generate operations for deleting logical router policy")
		return err
	}

	if err = c.Transact(ctx, "lr-policy-del", ops); err != nil {
		return err
	}

	return nil
}

func (c *OvnNbClient) GetLogicalRouterPolicyByUUID(ctx context.Context, uuid string) (*ovnnb.LogicalRouterPolicy, error) {
	policy := &ovnnb.LogicalRouterPolicy{UUID: uuid}
	if err := c.Get(ctx, policy); err != nil {
		return nil, err
	}

	return policy, nil
}

func (c *OvnNbClient) ListLogicalRouterPolicy(ctx context.Context, lrName string, filter func(policy *ovnnb.LogicalRouterPolicy) bool) ([]*ovnnb.LogicalRouterPolicy, error) {
	lr, err := c.GetLogicalRouter(ctx, lrName, false)
	if err != nil {
		return nil, err
	}
	policyList := make([]*ovnnb.LogicalRouterPolicy, 0, len(lr.Policies))
	for _, uuid := range lr.Policies {
		policy, err := c.GetLogicalRouterPolicyByUUID(ctx, uuid)
		if err != nil {
			if errors.Is(err, client.ErrNotFound) {
				continue
			}
			return nil, err
		}
		if filter == nil || filter(policy) {
			policyList = append(policyList, policy)
		}
	}
	return policyList, nil
}

func (c *OvnNbClient) DeleteLogicalRouterPolicyByFilter(ctx context.Context, lrName string, filter func(policy *ovnnb.LogicalRouterPolicy) bool) error {
	logger := log.FromContext(ctx)

	policies, err := c.ListLogicalRouterPolicy(ctx, lrName, filter)
	if err != nil {
		logger.Error(err, "failed to list logical router policies")
		return err
	}
	for _, policy := range policies {
		if err = c.deleteLogicalRouterPolicyByUUID(ctx, lrName, policy.UUID); err != nil {
			logger.Error(err, "failed to delete logical router policy", "uuid", policy.UUID)
			return err
		}
	}

	return nil
}

func (c *OvnNbClient) DeleteLogicalRouterPolicyByUUID(ctx context.Context, lrName string, policyUUID string) error {
	var (
		logger = log.FromContext(ctx)
		err    error
	)
	mutationFunc := func(lr *ovnnb.LogicalRouter) *model.Mutation {
		return &model.Mutation{
			Field:   &lr.Policies,
			Mutator: ovsdb.MutateOperationDelete,
			Value:   []string{policyUUID},
		}
	}
	ops, err := c.MutateLogicalRouterOps(ctx, lrName, mutationFunc)
	if err != nil {
		logger.Error(err, "failed to generate operations for deleting logical router policy")
		return err
	}
	if err = c.Transact(ctx, "lr-policy-del", ops); err != nil {
		return err
	}
	return nil
}
