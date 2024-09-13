package daemon

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-iptables/iptables"
	"k8s.io/apimachinery/pkg/util/wait"
	coreinformer "k8s.io/client-go/informers/core/v1"
	corelister "k8s.io/client-go/listers/core/v1"
	"k8s.io/klog/v2"

	ovninformer "github.com/fengjinlin/kube-ovn/pkg/client/informers/externalversions/kubeovn/v1"
	ovnlister "github.com/fengjinlin/kube-ovn/pkg/client/listers/kubeovn/v1"
	"github.com/fengjinlin/kube-ovn/pkg/consts"
	"github.com/fengjinlin/kube-ovn/pkg/utils"
)

type IPTablesWorker interface {
	Run(stopCh <-chan struct{})
}

func NewIPTablesWorker(config *Configuration,
	subnetInformer ovninformer.SubnetInformer,
	nodeInformer coreinformer.NodeInformer,
	protocol string,
	ipSetsMgr IPSetsWorker) (IPTablesWorker, error) {

	m := &iptablesWorker{
		config:        config,
		subnetsLister: subnetInformer.Lister(),
		nodesLister:   nodeInformer.Lister(),
		protocol:      protocol,
		ipSetsMgr:     ipSetsMgr,

		iptables: make(map[string]*iptables.IPTables),
	}

	if m.protocol == utils.ProtocolIPv4 || m.protocol == utils.ProtocolDual {
		ipt, err := iptables.NewWithProtocol(iptables.ProtocolIPv4)
		if err != nil {
			return nil, err
		}
		m.iptables[utils.ProtocolIPv4] = ipt
	}
	if m.protocol == utils.ProtocolIPv6 || m.protocol == utils.ProtocolDual {
		ipt, err := iptables.NewWithProtocol(iptables.ProtocolIPv6)
		if err != nil {
			return nil, err
		}
		m.iptables[utils.ProtocolIPv6] = ipt
	}

	return m, nil
}

type iptablesWorker struct {
	config *Configuration

	subnetsLister ovnlister.SubnetLister
	nodesLister   corelister.NodeLister

	protocol string

	ipSetsMgr IPSetsWorker

	iptables map[string]*iptables.IPTables
}

func (w *iptablesWorker) Run(stopCh <-chan struct{}) {
	go wait.Until(func() {
		if err := w.setUpIPTables(); err != nil {
			klog.Error("failed to setup iptables", err)
		}
	}, 3*time.Second, stopCh)

	<-stopCh
	klog.Info("stopping iptables manager")
}

func (w *iptablesWorker) setUpIPTables() error {
	klog.V(3).Infoln("start to set up iptables")

	node, err := w.nodesLister.Get(w.config.NodeName)
	if err != nil {
		klog.Errorf("failed to get node %s, %v", w.config.NodeName, err)
		return err
	}
	v4NodeIP, v6NodeIP := utils.GetNodeInternalIP(node)

	var (
		natOvnPreRoutingRulesTpl = []utils.IPTableRule{
			// mark packets from pod to service
			{
				Table: consts.TableNat,
				Chain: consts.ChainOvnPreRouting,
				Rule:  `-i ovn0 -m set --match-set {SUBNET} src -m set --match-set {SERVICE} dst -j MARK --set-xmark 0x4000/0x4000`,
			},
		}

		natOvnPostRoutingRulesTpl = []utils.IPTableRule{
			// nat packets marked by kube-proxy or kube-ovn
			{
				Table: consts.TableNat,
				Chain: consts.ChainOvnPostRouting,
				Rule:  `-m mark --mark 0x4000/0x4000 -j ` + consts.ChainOvnMasquerade,
			},
			// nat service traffic
			{
				Table: consts.TableNat,
				Chain: consts.ChainOvnPostRouting,
				Rule:  `-m set --match-set {SUBNET} src -m set --match-set {SUBNET} dst -j ` + consts.ChainOvnMasquerade,
			},
			// do not nat node port service traffic with external traffic policy set to local
			{
				Table: consts.TableNat,
				Chain: consts.ChainOvnPostRouting,
				Rule:  `-m mark --mark 0x80000/0x80000 -m set --match-set {SUBNET-DISTRIBUTED-GW} dst -j RETURN`,
			},
			// nat node port service traffic with external traffic policy set to local for subnets with centralized gateway
			{
				Table: consts.TableNat,
				Chain: consts.ChainOvnPostRouting,
				Rule:  `-m mark --mark 0x80000/0x80000 -j ` + consts.ChainOvnMasquerade,
			},
			// do not nat reply packets in direct routing
			{
				Table: consts.TableNat,
				Chain: consts.ChainOvnPostRouting,
				Rule:  `-p tcp -m tcp --tcp-flags SYN NONE -m conntrack --ctstate NEW -j RETURN`,
			},
			// do not nat route traffic
			{
				Table: consts.TableNat,
				Chain: consts.ChainOvnPostRouting,
				Rule:  `-m set ! --match-set {SUBNET} src -m set ! --match-set {OTHER-NODE} src -m set --match-set {SUBNET-NAT} dst -j RETURN`,
			},
			// default nat outgoing rules
			{
				Table: consts.TableNat,
				Chain: consts.ChainOvnPostRouting,
				Rule:  `-m set --match-set {SUBNET-NAT} src -m set ! --match-set {SUBNET} dst -j ` + consts.ChainOvnMasquerade,
			},
		}

		natOvnMasqueradeRulesTpl = []utils.IPTableRule{
			// clear mark
			{
				Table: consts.TableNat,
				Chain: consts.ChainOvnMasquerade,
				Rule:  `-j MARK --set-xmark 0x0/0xffffffff`,
			},
			// do masquerade
			{
				Table: consts.TableNat,
				Chain: consts.ChainOvnMasquerade,
				Rule:  `-j MASQUERADE`,
			},
		}

		mangleOvnPostRoutingRulesTpl = []utils.IPTableRule{
			// Drop invalid rst
			{
				Table: consts.TableMangle,
				Chain: consts.ChainPostRouting,
				Rule:  `-p tcp -m set --match-set {SUBNET} src -m tcp --tcp-flags RST RST -m state --state INVALID -j DROP`,
			},
		}

		nativeRulesTpl = []utils.IPTableRule{
			// Input Accept
			{Table: consts.TableFilter, Chain: consts.ChainInput, Rule: `-m set --match-set {SUBNET} src -j ACCEPT`},
			{Table: consts.TableFilter, Chain: consts.ChainInput, Rule: `-m set --match-set {SUBNET} dst -j ACCEPT`},
			{Table: consts.TableFilter, Chain: consts.ChainInput, Rule: `-m set --match-set {SERVICE} src -j ACCEPT`},
			{Table: consts.TableFilter, Chain: consts.ChainInput, Rule: `-m set --match-set {SERVICE} dst -j ACCEPT`},

			// Forward Accept
			{Table: consts.TableFilter, Chain: consts.ChainForward, Rule: `-m set --match-set {SUBNET} src -j ACCEPT`},
			{Table: consts.TableFilter, Chain: consts.ChainForward, Rule: `-m set --match-set {SUBNET} dst -j ACCEPT`},
			{Table: consts.TableFilter, Chain: consts.ChainForward, Rule: `-m set --match-set {SERVICE} src -j ACCEPT`},
			{Table: consts.TableFilter, Chain: consts.ChainForward, Rule: `-m set --match-set {SERVICE} dst -j ACCEPT`},

			// Output unmark to bypass kernel nat checksum issue https://github.com/flannel-io/flannel/issues/1279
			{Table: consts.TableFilter, Chain: consts.ChainOutput, Rule: `-p udp -m udp --dport 6081 -j MARK --set-xmark 0x0`},
		}
	)

	matchSetMapTpl := map[string]string{
		"{SUBNET}":                consts.IPSetSubnetTpl,
		"{SERVICE}":               consts.IPSetServiceTpl,
		"{SUBNET-DISTRIBUTED-GW}": consts.IPSetSubnetDistributedGwTpl,
		"{SUBNET-NAT}":            consts.IPSetSubnetNatTpl,
		"{OTHER-NODE}":            consts.IPSetOtherNodeTpl,
	}

	for protocol, ipt := range w.iptables {
		natOvnPreRoutingRules := make([]utils.IPTableRule, len(natOvnPreRoutingRulesTpl))
		natOvnPostRoutingRules := make([]utils.IPTableRule, len(natOvnPostRoutingRulesTpl))
		natOvnMasqueradeRules := make([]utils.IPTableRule, len(natOvnMasqueradeRulesTpl))
		mangleOvnPostRoutingRules := make([]utils.IPTableRule, len(mangleOvnPostRoutingRulesTpl))
		nativeRules := make([]utils.IPTableRule, len(nativeRulesTpl))
		copy(natOvnPreRoutingRules, natOvnPreRoutingRulesTpl)
		copy(natOvnPostRoutingRules, natOvnPostRoutingRulesTpl)
		copy(natOvnMasqueradeRules, natOvnMasqueradeRulesTpl)
		copy(mangleOvnPostRoutingRules, mangleOvnPostRoutingRulesTpl)
		copy(nativeRules, nativeRulesTpl)

		var nodeIP string
		var ipSetProtocol string
		if protocol == utils.ProtocolIPv6 {
			ipSetProtocol = "6-"
			nodeIP = v6NodeIP
		} else {
			ipSetProtocol = ""
			nodeIP = v4NodeIP
		}

		matchSetMap := make(map[string]string, len(matchSetMapTpl))
		for k, v := range matchSetMapTpl {
			matchSetMap[k] = fmt.Sprintf(v, ipSetProtocol)
		}

		kubeProxyIPSet := fmt.Sprintf("KUBE-%sCLUSTER-IP", ipSetProtocol)
		exists, err := w.ipSetsMgr.IPSetExists(kubeProxyIPSet)
		if err != nil {
			klog.Errorf("failed to check existence of ipset %s: %v", kubeProxyIPSet, err)
			return err
		}
		if exists {
			natOvnPreRoutingRules[0].Rule = fmt.Sprintf(`-i ovn0 -m set --match-set {SUBNET} src -m set --match-set %s dst,dst -j MARK --set-xmark 0x4000/0x4000`, kubeProxyIPSet)
			rejectRule := `-p tcp -m mark ! --mark 0x4000/0x4000 -m set --match-set {SERVICE} dst -m conntrack --ctstate NEW -j REJECT`
			//obsoleteRejectRule := `-m mark ! --mark 0x4000/0x4000 -m set --match-set %s dst -m conntrack --ctstate NEW -j REJECT`
			nativeRules = append(nativeRules,
				utils.IPTableRule{Table: "filter", Chain: "INPUT", Rule: rejectRule},
				utils.IPTableRule{Table: "filter", Chain: "OUTPUT", Rule: rejectRule},
			)
		}

		if nodeIP != "" {
			rule := utils.IPTableRule{
				Table: consts.TableNat,
				Chain: consts.ChainOvnPostRouting,
				Rule:  fmt.Sprintf(`-m set --match-set {SERVICE} src -m set --match-set {SUBNET} dst -m mark --mark 0x4000/0x4000 -j SNAT --to-source %s`, nodeIP),
			}
			natOvnPostRoutingRules = append([]utils.IPTableRule{rule},
				natOvnPostRoutingRules...,
			)

			for _, p := range [...]string{"tcp", "udp"} {
				nodePortIPSet := fmt.Sprintf("KUBE-%sNODE-PORT-LOCAL-%s", ipSetProtocol, strings.ToUpper(p))
				exists, err := w.ipSetsMgr.IPSetExists(nodePortIPSet)
				if err != nil {
					klog.Errorf("failed to check existence of ipset %s: %v", nodePortIPSet, err)
					return err
				}
				if !exists {
					klog.V(5).Infof("ipset %s does not exist", nodePortIPSet)
					continue
				}
				rule := fmt.Sprintf("-p %s -m addrtype --dst-type LOCAL -m set --match-set %s dst -j MARK --set-xmark 0x80000/0x80000", p, nodePortIPSet)
				rule2 := fmt.Sprintf("-p %s -m set --match-set {OTHER-NODE} src -m set --match-set %s dst -j MARK --set-xmark 0x4000/0x4000", p, nodePortIPSet)
				natOvnPreRoutingRules = append(natOvnPreRoutingRules,
					utils.IPTableRule{Table: consts.TableNat, Chain: consts.ChainOvnPreRouting, Rule: rule},
					utils.IPTableRule{Table: consts.TableNat, Chain: consts.ChainOvnPreRouting, Rule: rule2},
				)
			}
		}

		nativeRules = append(nativeRules,
			utils.IPTableRule{
				Table: consts.TableFilter,
				Chain: consts.ChainForward,
				Rule:  `-m set --match-set {SUBNET} src`,
			},
			utils.IPTableRule{
				Table: consts.TableFilter,
				Chain: consts.ChainForward,
				Rule:  `-m set --match-set {SUBNET} dst`,
			},
		)

		for _, rule := range nativeRules {
			for k, v := range matchSetMap {
				rule.Rule = strings.ReplaceAll(rule.Rule, k, v)
			}
			if err = w.createIPTablesRule(ipt, rule); err != nil {
				klog.Errorf(`failed to create iptables rule "%s": %v`, rule.Rule, err)
				return err
			}
		}

		if err = w.updateIptablesChain(ipt, consts.TableNat, consts.ChainOvnPreRouting, consts.ChainPreRouting, natOvnPreRoutingRules, matchSetMap); err != nil {
			klog.Errorf("failed to update chain %s/%s: %v", consts.TableNat, consts.ChainOvnPreRouting, err)
			return err
		}
		if err = w.updateIptablesChain(ipt, consts.TableNat, consts.ChainOvnMasquerade, "", natOvnMasqueradeRules, matchSetMap); err != nil {
			klog.Errorf("failed to update chain %s/%s: %v", consts.TableNat, consts.ChainOvnMasquerade, err)
			return err
		}
		if err = w.updateIptablesChain(ipt, consts.TableNat, consts.ChainOvnPostRouting, consts.ChainPostRouting, natOvnPostRoutingRules, matchSetMap); err != nil {
			klog.Errorf("failed to update chain %s/%s: %v", consts.TableNat, consts.ChainOvnPostRouting, err)
			return err
		}
		if err = w.updateIptablesChain(ipt, consts.TableMangle, consts.ChainOvnPostRouting, consts.ChainPostRouting, mangleOvnPostRoutingRules, matchSetMap); err != nil {
			klog.Errorf("failed to update chain %s/%s: %v", consts.TableMangle, consts.ChainOvnPostRouting, err)
			return err
		}
	}

	return nil
}

func (w *iptablesWorker) updateIptablesChain(ipt *iptables.IPTables, table, chain, parentChain string, rules []utils.IPTableRule, matchSetMap map[string]string) error {
	ok, err := ipt.ChainExists(table, chain)
	if err != nil {
		klog.Errorf("failed to check existence of iptables chain %s in table %s: %v", chain, table, err)
		return err
	}
	if !ok {
		if err = ipt.NewChain(table, chain); err != nil {
			klog.Errorf("failed to create iptables chain %s in table %s: %v", chain, table, err)
			return err
		}
		klog.Infof("created iptables chain %s in table %s", chain, table)
	}
	if parentChain != "" {
		comment := fmt.Sprintf("kube-ovn %s rules", strings.ToLower(parentChain))
		rule := utils.IPTableRule{
			Table: table,
			Chain: parentChain,
			Rule:  fmt.Sprintf(`-m comment --comment "%s" -j %s`, comment, chain),
		}
		if err = w.createIPTablesRule(ipt, rule); err != nil {
			klog.Errorf("failed to create iptables rule: %v", err)
			return err
		}
	}

	// list existing rules
	ruleList, err := ipt.List(table, chain)
	if err != nil {
		klog.Errorf("failed to list iptables rules in chain %s/%s: %v", table, chain, err)
		return err
	}
	// filter the heading default chain policy: -N OVN-POSTROUTING
	ruleList = ruleList[1:]
	// trim prefix, eg: "-A OVN-POSTROUTING "
	prefixLen := 4 + len(chain)
	existingRules := make([][]string, 0, len(ruleList))
	for _, rule := range ruleList {
		existingRules = append(existingRules, utils.DoubleQuotedFields(rule[prefixLen:]))
	}
	var added int
	for i, rule := range rules {
		for k, v := range matchSetMap {
			rule.Rule = strings.ReplaceAll(rule.Rule, k, v)
		}

		ruleArr := strings.Fields(rule.Rule)
		if i-added < len(existingRules) && reflect.DeepEqual(existingRules[i-added], ruleArr) {
			klog.V(5).Infof("iptables rule `%s` already exists", rule.Rule)
			continue
		}
		klog.Infof("creating iptables rule in table %s chain %s at position %d: `%q`", table, chain, i+1, rule.Rule)
		if err = ipt.Insert(table, chain, i+1, ruleArr...); err != nil {
			klog.Errorf("failed to insert iptables rule `%v`: %v", rule.Rule, err)
			return err
		}
		added++
	}
	for i := len(existingRules) - 1; i >= len(rules)-added; i-- {
		if err = ipt.Delete(table, chain, strconv.Itoa(i+added+1)); err != nil {
			klog.Errorf("failed to delete iptables rule `%v`: %v", existingRules[i], err)
			return err
		}
		klog.Infof("deleted iptables rule in table %s chain %s: %q", table, chain, strings.Join(existingRules[i], " "))
	}

	return nil
}

func (w *iptablesWorker) createIPTablesRule(ipt *iptables.IPTables, rule utils.IPTableRule) error {
	ruleArr := utils.DoubleQuotedFields(rule.Rule)
	exists, err := ipt.Exists(rule.Table, rule.Chain, ruleArr...)
	if err != nil {
		klog.Errorf("failed to check iptables rule existence: %v", err)
		return err
	}

	if exists {
		klog.V(3).Infof(`iptables rule %q already exists`, rule.Rule)
		return nil
	}

	klog.Infof("creating iptables rule in table %s chain %s at position %d: %q", rule.Table, rule.Chain, 1, rule.Rule)
	if err = ipt.Insert(rule.Table, rule.Chain, 1, ruleArr...); err != nil {
		klog.Errorf(`failed to insert iptables rule "%s": %v`, rule.Rule, err)
		return err
	}

	return nil
}

//func isLegacyIptablesMode() (bool, error) {
//	path, err := utils.EvalCommandSymlinks("iptables")
//	if err != nil {
//		return false, err
//	}
//	pathLegacy, err := utils.EvalCommandSymlinks("iptables-legacy")
//	if err != nil {
//		return false, err
//	}
//	return path == pathLegacy, nil
//}
