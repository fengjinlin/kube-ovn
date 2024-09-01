// Code generated by "libovsdb.modelgen"
// DO NOT EDIT.

package vswitch

const CTTimeoutPolicyTable = "CT_Timeout_Policy"

type (
	CTTimeoutPolicyTimeouts = string
)

var (
	CTTimeoutPolicyTimeoutsTCPSynSent     CTTimeoutPolicyTimeouts = "tcp_syn_sent"
	CTTimeoutPolicyTimeoutsTCPSynRecv     CTTimeoutPolicyTimeouts = "tcp_syn_recv"
	CTTimeoutPolicyTimeoutsTCPEstablished CTTimeoutPolicyTimeouts = "tcp_established"
	CTTimeoutPolicyTimeoutsTCPFinWait     CTTimeoutPolicyTimeouts = "tcp_fin_wait"
	CTTimeoutPolicyTimeoutsTCPCloseWait   CTTimeoutPolicyTimeouts = "tcp_close_wait"
	CTTimeoutPolicyTimeoutsTCPLastAck     CTTimeoutPolicyTimeouts = "tcp_last_ack"
	CTTimeoutPolicyTimeoutsTCPTimeWait    CTTimeoutPolicyTimeouts = "tcp_time_wait"
	CTTimeoutPolicyTimeoutsTCPClose       CTTimeoutPolicyTimeouts = "tcp_close"
	CTTimeoutPolicyTimeoutsTCPSynSent2    CTTimeoutPolicyTimeouts = "tcp_syn_sent2"
	CTTimeoutPolicyTimeoutsTCPRetransmit  CTTimeoutPolicyTimeouts = "tcp_retransmit"
	CTTimeoutPolicyTimeoutsTCPUnack       CTTimeoutPolicyTimeouts = "tcp_unack"
	CTTimeoutPolicyTimeoutsUDPFirst       CTTimeoutPolicyTimeouts = "udp_first"
	CTTimeoutPolicyTimeoutsUDPSingle      CTTimeoutPolicyTimeouts = "udp_single"
	CTTimeoutPolicyTimeoutsUDPMultiple    CTTimeoutPolicyTimeouts = "udp_multiple"
	CTTimeoutPolicyTimeoutsICMPFirst      CTTimeoutPolicyTimeouts = "icmp_first"
	CTTimeoutPolicyTimeoutsICMPReply      CTTimeoutPolicyTimeouts = "icmp_reply"
)

// CTTimeoutPolicy defines an object in CT_Timeout_Policy table
type CTTimeoutPolicy struct {
	UUID        string            `ovsdb:"_uuid"`
	ExternalIDs map[string]string `ovsdb:"external_ids"`
	Timeouts    map[string]int    `ovsdb:"timeouts"`
}
