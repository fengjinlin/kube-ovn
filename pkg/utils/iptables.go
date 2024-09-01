package utils

// IPTableRule wraps iptables rule
type IPTableRule struct {
	Table string
	Chain string
	Rule  string
}

type GwIPTableCounters struct {
	Packets     int
	PacketBytes int
}
