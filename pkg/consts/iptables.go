package consts

const (
	TableNat    = "nat"
	TableFilter = "filter"
	TableMangle = "mangle"
	TableRaw    = "raw"

	ChainPreRouting  = "PREROUTING"
	ChainInput       = "INPUT"
	ChainOutput      = "OUTPUT"
	ChainPostRouting = "POSTROUTING"
	ChainForward     = "FORWARD"

	ChainOvnPreRouting  = "OVN-PREROUTING"
	ChainOvnPostRouting = "OVN-POSTROUTING"
	ChainOvnMasquerade  = "OVN-MASQUERADE"
)
