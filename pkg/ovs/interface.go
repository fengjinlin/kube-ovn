package ovs

import (
	"context"
	"github.com/ovn-org/libovsdb/ovsdb"
)

type Common interface {
	Transact(ctx context.Context, method string, operations []ovsdb.Operation) error
	NamedUUID() string
}
