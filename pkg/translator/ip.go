package translator

import (
	"context"
	"github.com/fengjinlin/kube-ovn/pkg/ovsdb/ovnnb"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ipTranslator struct {
	translator
}

func (t *ipTranslator) CleanupPort(ctx context.Context, portName, portMac string) (bool, error) {
	var (
		logger = log.FromContext(ctx)
	)

	if ports, err := t.ovnNbClient.ListLogicalSwitchPorts(ctx, func(lsp *ovnnb.LogicalSwitchPort) bool {
		if lsp.Name == portName {
			return true
		}
		return false
	}); err != nil {
		logger.Error(err, "failed to list logical switch port", "lspName", portName)
		return false, err
	} else {
		var hit bool
		for _, port := range ports {
			if utils.ContainsString(port.Addresses, portMac) {
				hit = true
				if err = t.ovnNbClient.DeleteLogicalSwitchPort(ctx, portName); err != nil {
					logger.Error(err, "failed to delete logical switch port", "lspName", portName)
					return false, err
				}
			}
		}
		return hit, nil
	}
}
